package vchat

import (
	"bytes"
	"encoding"
	"encoding/binary"
	"io"

	"github.com/satori/go.uuid"
)

type PacketType uint8

const (
	PacketJoin PacketType = iota + 1
	PacketLeave
	PacketAudio
)

type Packet struct {
	Type        PacketType
	ClientID    uuid.UUID
	AudioFrames []AudioFrame
}

var (
	_ io.WriterTo                = (*Packet)(nil)
	_ io.ReaderFrom              = (*Packet)(nil)
	_ encoding.BinaryMarshaler   = (*Packet)(nil)
	_ encoding.BinaryUnmarshaler = (*Packet)(nil)
)

func (p *Packet) BinarySize() int {
	// Packet type + ClientID + AudioFrames
	return 1 + len(p.ClientID) + p.audioFramesSize()
}

func (p *Packet) audioFramesSize() int {
	size := 0
	for _, frame := range p.AudioFrames {
		size += frame.BinarySize()
	}

	return size
}

func (p *Packet) WriteTo(w io.Writer) (int64, error) {
	var err error

	err = binary.Write(w, binary.BigEndian, p.Type)
	if err != nil {
		return 0, err
	}

	err = binary.Write(w, binary.BigEndian, p.ClientID)
	if err != nil {
		return 0, err
	}

	audioSize := uint16(p.audioFramesSize())
	err = binary.Write(w, binary.BigEndian, audioSize)

	for _, frame := range p.AudioFrames {
		_, err = frame.WriteTo(w)
		if err != nil {
			return 0, err
		}
	}

	return int64(p.BinarySize()), nil
}

func (p *Packet) ReadFrom(r io.Reader) (int64, error) {
	var err error

	var pt PacketType
	err = binary.Read(r, binary.BigEndian, &pt)
	if err != nil {
		return 0, err
	}
	p.Type = pt

	var clientID uuid.UUID
	err = binary.Read(r, binary.BigEndian, &clientID)
	if err != nil {
		return 0, err
	}
	p.ClientID = clientID

	var audioLength uint16
	err = binary.Read(r, binary.BigEndian, &audioLength)
	if err != nil {
		return 0, err
	}

	data := make([]byte, audioLength)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return 0, err
	}
	buf := bytes.NewBuffer(data)

	for {
		a := AudioFrame{}
		_, err := a.ReadFrom(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, err
		}
		p.AudioFrames = append(p.AudioFrames, a)
	}

	return int64(p.BinarySize()), nil
}

func (p *Packet) MarshalBinary() ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, p.BinarySize()))
	_, err := p.WriteTo(buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *Packet) UnmarshalBinary(data []byte) error {
	r := bytes.NewReader(data)
	_, err := p.ReadFrom(r)
	if err != nil {
		return err
	}

	return nil
}

type AudioFrame struct {
	Data []byte
}

var (
	_ io.WriterTo                = (*AudioFrame)(nil)
	_ io.ReaderFrom              = (*AudioFrame)(nil)
	_ encoding.BinaryMarshaler   = (*AudioFrame)(nil)
	_ encoding.BinaryUnmarshaler = (*AudioFrame)(nil)
)

func (a *AudioFrame) BinarySize() int {
	// length + data
	return 2 + len(a.Data)
}

func (a *AudioFrame) WriteTo(w io.Writer) (int64, error) {
	var err error

	length := uint16(len(a.Data))
	if length == 0 {
		return 0, nil
	}

	err = binary.Write(w, binary.BigEndian, length)

	_, err = w.Write(a.Data)
	if err != nil {
		return 0, err
	}

	return int64(a.BinarySize()), nil
}

func (a *AudioFrame) ReadFrom(r io.Reader) (int64, error) {
	var err error

	var length uint16
	err = binary.Read(r, binary.BigEndian, &length)
	if err != nil {
		return 0, err
	}

	data := make([]byte, length)
	_, err = io.ReadFull(r, data)
	if err != nil {
		return 0, err
	}
	a.Data = data

	return int64(a.BinarySize()), nil
}

func (a *AudioFrame) MarshalBinary() ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, a.BinarySize()))
	_, err := a.WriteTo(buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (a *AudioFrame) UnmarshalBinary(data []byte) error {
	r := bytes.NewReader(data)
	_, err := a.ReadFrom(r)
	if err != nil {
		return err
	}

	return nil
}
