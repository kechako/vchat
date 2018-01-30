package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/gordonklaus/portaudio"
)

type process struct {
	addr   string // address to connect
	echo   bool
	client *Client

	stream *portaudio.Stream
	inbuf  []int16
	outbuf []int16

	inputReader  *io.PipeReader
	inputWriter  *io.PipeWriter
	outputReader *io.PipeReader
	outputWriter *io.PipeWriter

	inputSize   uint64
	sendSize    uint64
	receiveSize uint64
	outputSize  uint64

	wg   *sync.WaitGroup
	exit chan struct{}
}

func newProcess() *process {
	ir, iw := io.Pipe()
	or, ow := io.Pipe()

	p := &process{
		exit:         make(chan struct{}),
		inputReader:  ir,
		inputWriter:  iw,
		outputReader: or,
		outputWriter: ow,
		wg:           &sync.WaitGroup{},
	}

	ch := make(chan os.Signal)
	go p.waitSignal(ch)
	signal.Notify(ch, os.Interrupt)

	return p
}

func (p *process) parseArgs() error {
	var addr string
	var echo bool
	flag.StringVar(&addr, "addr", "localhost:8080", "address to connect")
	flag.BoolVar(&echo, "echo", false, "echo")
	flag.Parse()

	if addr == "" {
		return errors.New("invalid address")
	}

	p.addr = addr
	p.echo = echo

	return nil
}

func (p *process) waitSignal(ch <-chan os.Signal) {
	defer close(p.exit)
	<-ch
}

func (p *process) run() error {
	var err error

	p.client, err = NewClient(p.addr)
	if err != nil {
		return err
	}
	defer p.client.Close()

	p.client.Echo = p.echo

	p.client.Join()
	defer p.client.Leave()

	defer p.wg.Wait()

	// initialize portaudio
	portaudio.Initialize()
	defer portaudio.Terminate()

	const bitDepth = 16
	const channels = 1
	const sampleRate = 8000
	const bufferLength = 500

	// open default stream
	p.inbuf = make([]int16, bufferLength)
	p.outbuf = make([]int16, bufferLength)
	p.stream, err = portaudio.OpenDefaultStream(channels, channels, sampleRate, len(p.inbuf), p.inbuf, p.outbuf)
	if err != nil {
		return err
	}
	defer p.stream.Close()

	err = p.stream.Start()
	if err != nil {
		return err
	}
	defer p.stream.Stop()

	p.wg.Add(5)
	go p.monitorLoop()
	go p.inputLoop()
	go p.sendLoop()
	go p.receiveLoop()
	go p.outputLoop()

	<-p.exit

	p.closePipe()

	return nil
}
func (p *process) closePipe() {
	p.inputReader.Close()
	p.inputWriter.Close()
	p.outputReader.Close()
	p.outputWriter.Close()
}

func (p *process) wait() {
	p.wg.Wait()
}

func (p *process) inputLoop() {
	defer p.wg.Done()

	for {
		select {
		case _, ok := <-p.exit:
			if !ok {
				return
			}
		default:
			var err error
			err = p.stream.Read()
			if err != nil && err != portaudio.InputOverflowed {
				log.Printf("error read from audio stream : %v", err)
				return
			}

			p.inputSize += uint64(len(p.inbuf) * 2)

			err = binary.Write(p.inputWriter, binary.LittleEndian, p.inbuf)
			if err != nil {
				if err != io.ErrClosedPipe {
					log.Printf("error write to input pipe : %v", err)
				}
				return
			}
		}
	}
}

func (p *process) sendLoop() {
	defer p.wg.Done()

	data := make([]byte, 1000)
	for {
		select {
		case _, ok := <-p.exit:
			if !ok {
				return
			}
		default:
			for offset := 0; offset < len(data); {
				n, err := p.inputReader.Read(data[offset:len(data)])
				if err != nil {
					if err != io.ErrClosedPipe {
						log.Printf("error read from input pipe : %v", err)
					}
					return
				}
				offset += n
			}
			p.client.Send(data)
			p.sendSize += uint64(len(data))
		}
	}
}

func (p *process) receiveLoop() {
	defer p.wg.Done()

	for {
		select {
		case _, ok := <-p.exit:
			if !ok {
				return
			}
		case data := <-p.client.Receive:
			for offset := 0; offset < len(data); {
				n, err := p.outputWriter.Write(data[offset:len(data)])
				if err != nil {
					if err != io.ErrClosedPipe {
						log.Printf("error write to output pipe : %v", err)
					}
					return
				}
				offset += n
			}
			p.receiveSize += uint64(len(data))
		}
	}
}

func (p *process) outputLoop() {
	defer p.wg.Done()

	for {
		select {
		case _, ok := <-p.exit:
			if !ok {
				return
			}
		default:
			err := binary.Read(p.outputReader, binary.LittleEndian, &p.outbuf)
			if err != nil {
				if err != io.ErrClosedPipe {
					log.Printf("error read from output pipe : %v", err)
				}
				return
			}
			err = p.stream.Write()
			if err != nil {
				log.Printf("error write to audio stream : %v", err)
				return
			}
			p.outputSize += uint64(len(p.outbuf) * 2)
		}
	}
}

func (p *process) monitorLoop() {
	defer p.wg.Done()

	t := time.NewTicker(time.Second)
	defer t.Stop()

	prevTime := time.Now()
	var input, send, receive, output uint64
	for {
		select {
		case _, ok := <-p.exit:
			if !ok {
				return
			}
		case now := <-t.C:
			fmt.Printf("Input : %d, Send : %d, Receive : %d, Output : %d\n", p.inputSize, p.sendSize, p.receiveSize, p.outputSize)
			inputRate := float64(p.inputSize-input) / now.Sub(prevTime).Seconds()
			sendRate := float64(p.sendSize-send) / now.Sub(prevTime).Seconds()
			receiveRate := float64(p.receiveSize-receive) / now.Sub(prevTime).Seconds()
			outputRate := float64(p.outputSize-output) / now.Sub(prevTime).Seconds()
			fmt.Printf("Input : %f/s, Send : %f/s, Receive : %f/s, Output : %f/s\n", inputRate, sendRate, receiveRate, outputRate)

			input = p.inputSize
			send = p.sendSize
			receive = p.receiveSize
			output = p.outputSize
			prevTime = now
		}
	}
}

func run() (int, error) {
	var err error

	p := newProcess()

	err = p.parseArgs()
	if err != nil {
		return 2, err
	}

	err = p.run()
	if err != nil {
		return 1, err
	}

	return 0, nil
}

func main() {
	code, err := run()
	if err != nil {
		log.Printf("error : %v", err)
	}
	if code != 0 {
		os.Exit(code)
	}
}
