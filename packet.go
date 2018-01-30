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
	Type     PacketType
	ClientID uuid.UUID
	Data     []byte
}

var (
	_ io.WriterTo                = (*Packet)(nil)
	_ io.ReaderFrom              = (*Packet)(nil)
	_ encoding.BinaryMarshaler   = (*Packet)(nil)
	_ encoding.BinaryUnmarshaler = (*Packet)(nil)
)

func (p *Packet) binarySize() int {
	return 1 + len(p.ClientID) + 2 + len(p.Data)
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

	length := uint16(len(p.Data))
	err = binary.Write(w, binary.BigEndian, length)

	for offset := 0; offset < len(p.Data); {
		n, err := w.Write(p.Data[offset:len(p.Data)])
		if err != nil {
			return 0, err
		}
		offset += n
	}

	return int64(p.binarySize()), nil
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

	var length uint16
	err = binary.Read(r, binary.BigEndian, &length)
	if err != nil {
		return 0, err
	}

	data := make([]byte, length)
	for offset := 0; offset < int(length); {
		n, err := r.Read(data[offset:length])
		if err != nil {
			return 0, err
		}
		offset += n
	}
	p.Data = data

	return int64(p.binarySize()), nil
}

func (p *Packet) MarshalBinary() ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, p.binarySize()))
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
