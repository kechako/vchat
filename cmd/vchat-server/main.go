package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"

	"github.com/kechako/vchat"
	"github.com/satori/go.uuid"
)

type Room struct {
	conn *net.UDPConn

	join    chan *Client
	leave   chan *Client
	clients sync.Map

	forward chan *vchat.Packet

	exit chan struct{}
	wg   *sync.WaitGroup
}

func NewRoom() *Room {
	return &Room{
		join:    make(chan *Client),
		leave:   make(chan *Client),
		forward: make(chan *vchat.Packet),
		exit:    make(chan struct{}),
		wg:      &sync.WaitGroup{},
	}
}

func (r *Room) Serve(address string) error {
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	r.conn = conn

	log.Printf("start server [%v]", udpAddr)

	r.wg.Add(2)
	go r.run()
	go r.read()

	return nil
}

func (r *Room) Close() error {
	close(r.exit)
	err := r.conn.Close()
	r.wg.Wait()

	return err
}

func (r *Room) read() {
	defer r.wg.Done()
	buf := make([]byte, 1500)
	for {
		select {
		case _, ok := <-r.exit:
			if !ok {
				return
			}
		default:
			n, addr, err := r.conn.ReadFromUDP(buf)
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
					// ignore error
				} else {
					log.Printf("error read udp : %v", err)
				}
				break
			}
			var p vchat.Packet
			err = p.UnmarshalBinary(buf[0:n])
			if err != nil {
				log.Printf("error unmarshal packet : %v", err)
				break
			}
			switch p.Type {
			case vchat.PacketJoin:
				fmt.Println(hex.Dump(buf[0:n]))
				c := &Client{
					ID:   p.ClientID,
					Addr: addr,
				}
				r.join <- c
			case vchat.PacketLeave:
				fmt.Println(hex.Dump(buf[0:n]))
				c := &Client{
					ID:   p.ClientID,
					Addr: addr,
				}
				r.leave <- c
			case vchat.PacketAudio:
				r.forward <- &p
			default:
				log.Print("error : invalid packet")
			}
		}
	}
}

func (r *Room) run() {
	defer r.wg.Done()
	for {
		select {
		case _, ok := <-r.exit:
			if !ok {
				return
			}
		case c := <-r.join:
			r.clients.Store(c.ID, c)
			log.Printf("Client joined : %v [%v]", c.ID, c.Addr)
		case c := <-r.leave:
			r.clients.Delete(c.ID)
			log.Printf("Client leaved : %v [%v]", c.ID, c.Addr)
		case p := <-r.forward:
			buf, err := p.MarshalBinary()
			if err != nil {
				log.Printf("Error marshal packet : %v", err)
				break
			}

			r.clients.Range(func(key, value interface{}) bool {
				c := value.(*Client)
				r.conn.WriteToUDP(buf, c.Addr)
				return true
			})
		}
	}
}

type Client struct {
	ID   uuid.UUID
	Addr *net.UDPAddr
}

func run() (int, error) {
	var addr string
	flag.StringVar(&addr, "addr", ":8080", "address to listen")
	flag.Parse()

	if addr == "" {
		return 0, errors.New("invalid address")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	ch := make(chan os.Signal)
	go func(wg *sync.WaitGroup) {
		defer wg.Done()
		<-ch
	}(&wg)
	signal.Notify(ch, os.Interrupt)

	r := NewRoom()
	err := r.Serve(addr)
	if err != nil {
		return 1, err
	}
	defer r.Close()

	wg.Wait()

	return 0, nil
}

func main() {
	code, err := run()
	if err != nil {
		log.Printf("error : %v\n", err)
	}
	if code != 0 {
		os.Exit(code)
	}
}
