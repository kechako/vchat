package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/gordonklaus/portaudio"
	"github.com/hraban/opus"
	"github.com/kechako/vchat"
)

const (
	DefaultBitDepth       = 16
	DefaultChannels       = 1
	DefaultSampleRate     = 48000
	DefaultFrameSize      = 480
	DefaultOpusDataLength = 1000
)

type process struct {
	addr   string // address to connect
	echo   bool
	client *Client

	channels       int
	sampleRate     int
	frameSize      int
	frameSizeMs    float32
	opusDataLength int

	stream *portaudio.Stream
	inbuf  []int16
	outbuf []int16

	opusEnc *opus.Encoder
	opusDec *opus.Decoder

	inputSize   uint64
	sendSize    uint64
	receiveSize uint64
	outputSize  uint64

	wg   *sync.WaitGroup
	exit chan struct{}
}

func newProcess() (p *process, err error) {
	p = &process{
		channels:       DefaultChannels,
		sampleRate:     DefaultSampleRate,
		frameSize:      DefaultFrameSize,
		opusDataLength: DefaultOpusDataLength,
		wg:             &sync.WaitGroup{},
		exit:           make(chan struct{}),
	}

	err = p.parseArgs()
	if err != nil {
		return
	}

	err = p.init()
	if err != nil {
		return
	}

	ch := make(chan os.Signal)
	go p.waitSignal(ch)
	signal.Notify(ch, os.Interrupt)

	return
}

func (p *process) init() error {
	frameSizeMs := float32(p.frameSize) / float32(p.channels) * 1000 / float32(p.sampleRate)
	switch frameSizeMs {
	case 2.5, 5, 10, 20, 40, 60:
		// Good.
	default:
		return fmt.Errorf("Illegal frame size: %d bytes (%f ms)", p.frameSize, frameSizeMs)
	}
	p.frameSizeMs = frameSizeMs

	p.inbuf = make([]int16, p.frameSize)
	p.outbuf = make([]int16, p.frameSize)

	return nil
}

type ParseFlagsError string

func (err ParseFlagsError) Error() string {
	return string(err)
}

func (p *process) parseArgs() error {
	var addr string
	var echo bool
	var channels int
	var sampleRate int
	var frameSize int
	flag.StringVar(&addr, "addr", "localhost:8080", "address to connect")
	flag.BoolVar(&echo, "echo", false, "echo")
	flag.IntVar(&channels, "channels", DefaultChannels, "Audio channels")
	flag.IntVar(&sampleRate, "rate", DefaultSampleRate, "Audio sample rate")
	flag.IntVar(&frameSize, "fsize", DefaultFrameSize, "Audio frame size")
	flag.Parse()

	if addr == "" {
		return ParseFlagsError("invalid address")
	}

	p.addr = addr
	p.echo = echo
	p.channels = channels
	p.sampleRate = sampleRate
	p.frameSize = frameSize

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

	// open default stream
	p.stream, err = portaudio.OpenDefaultStream(p.channels, p.channels, float64(p.sampleRate), len(p.inbuf), p.inbuf, p.outbuf)
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
	go p.inputSendLoop()
	go p.receiveOutputLoop()

	<-p.exit

	fmt.Println()
	log.Print("Exit...")

	return nil
}

func (p *process) wait() {
	p.wg.Wait()
}

func (p *process) inputSendLoop() {
	defer p.wg.Done()

	enc, err := opus.NewEncoder(p.sampleRate, p.channels, opus.AppVoIP)
	if err != nil {
		panic(err)
	}

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

			p.inputSize += uint64(p.frameSize * 2)

			data := make([]byte, p.opusDataLength)
			n, err := enc.Encode(p.inbuf, data)
			if err != nil {
				log.Printf("error encode opus : %v", err)
				return
			}

			p.client.Send(vchat.AudioFrame{
				Data: data[:n],
			})
			p.sendSize += uint64(n)
		}
	}
}

func (p *process) receiveOutputLoop() {
	defer p.wg.Done()

	dec, err := opus.NewDecoder(p.sampleRate, p.channels)
	if err != nil {
		panic(err)
	}

	for {
		select {
		case _, ok := <-p.exit:
			if !ok {
				return
			}
		case frame := <-p.client.Receive:
			p.receiveSize += uint64(len(frame.Data))

			_, err = dec.Decode(frame.Data, p.outbuf)
			if err != nil {
				log.Printf("error decode opus : %v", err)
				return
			}

			err = p.stream.Write()
			if err != nil && err != portaudio.OutputUnderflowed {
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
			inputRate := float64(p.inputSize-input) / now.Sub(prevTime).Seconds()
			sendRate := float64(p.sendSize-send) / now.Sub(prevTime).Seconds()
			receiveRate := float64(p.receiveSize-receive) / now.Sub(prevTime).Seconds()
			outputRate := float64(p.outputSize-output) / now.Sub(prevTime).Seconds()
			fmt.Printf("\rInput : %s (%s/s), Send : %s (%s/s), Receive : %s (%s/s), Output : %s (%s/s)",
				humanize.Bytes(p.inputSize),
				humanize.Bytes(uint64(inputRate)),
				humanize.Bytes(p.sendSize),
				humanize.Bytes(uint64(sendRate)),
				humanize.Bytes(p.receiveSize),
				humanize.Bytes(uint64(receiveRate)),
				humanize.Bytes(p.outputSize),
				humanize.Bytes(uint64(outputRate)))

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

	p, err := newProcess()
	if err != nil {
		if _, ok := err.(ParseFlagsError); ok {
			return 2, err
		}
		return 1, err
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
