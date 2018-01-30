package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/kechako/vchat"
	"github.com/satori/go.uuid"
)

var DefaultReceiveBufferSize = 1024
var DefaultSendBufferSize = 1024

type Client struct {
	ClientID uuid.UUID
	Receive  chan []byte
	send     chan []byte

	Echo bool

	remoteAddr *net.UDPAddr
	conn       *net.UDPConn

	exit chan struct{}
	wg   *sync.WaitGroup
}

func NewClient(addr string) (*Client, error) {
	fmt.Println(vchat.PacketJoin, vchat.PacketLeave, vchat.PacketAudio)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}

	c := &Client{
		ClientID:   uuid.NewV4(),
		Receive:    make(chan []byte, DefaultReceiveBufferSize),
		send:       make(chan []byte, DefaultSendBufferSize),
		remoteAddr: udpAddr,
		conn:       conn,
		exit:       make(chan struct{}),
		wg:         &sync.WaitGroup{},
	}

	c.wg.Add(2)
	go c.sendLoop()
	go c.readLoop()

	return c, nil
}

func (c *Client) Close() error {
	close(c.exit)
	err := c.conn.Close()
	c.wg.Wait()

	return err
}

func (c *Client) Join() error {
	p := vchat.Packet{
		ClientID: c.ClientID,
		Type:     vchat.PacketJoin,
	}
	buf, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	fmt.Println(hex.Dump(buf))
	_, err = c.conn.Write(buf)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) Leave() error {
	p := vchat.Packet{
		ClientID: c.ClientID,
		Type:     vchat.PacketLeave,
	}
	buf, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	fmt.Println(hex.Dump(buf))
	_, err = c.conn.Write(buf)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) Send(data []byte) {
	c.send <- data
}

func (c *Client) sendLoop() {
	defer c.wg.Done()
	buf := bytes.NewBuffer(make([]byte, 0, 1500))
	for {
		select {
		case _, ok := <-c.exit:
			if !ok {
				return
			}
		case data := <-c.send:
			p := vchat.Packet{
				ClientID: c.ClientID,
				Type:     vchat.PacketAudio,
				Data:     data,
			}
			buf.Reset()
			_, err := p.WriteTo(buf)
			if err != nil {
				log.Printf("error : %v", err)
				break
			}

			sendData := buf.Bytes()
			_, err = c.conn.Write(sendData)
			if err != nil {
				log.Printf("error : %v", err)
				break
			}
		}
	}
}

func (c *Client) readLoop() {
	defer c.wg.Done()
	buf := make([]byte, 1500)
	for {
		select {
		case _, ok := <-c.exit:
			if !ok {
				return
			}
		default:
			n, err := c.conn.Read(buf)
			if err != nil {
				if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
					// ignore
				} else {
					log.Printf("error : %v", err)
				}
				break
			}
			var p vchat.Packet
			err = p.UnmarshalBinary(buf[0:n])
			if err != nil {
				log.Printf("error : %v", err)
				break
			}

			if p.Type != vchat.PacketAudio {
				// ignore
				break
			}

			if !c.Echo && uuid.Equal(p.ClientID, c.ClientID) {
				// ignore
				break
			}

			c.Receive <- p.Data
		}
	}
}
