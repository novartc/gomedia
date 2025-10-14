package rtp

import (
	"bytes"
	"errors"
)

// VP8UnPacker for vp8
// RFC 7741
type VP8UnPacker struct {
	CommUnPacker
	frameBuffer  *bytes.Buffer
	timestamp    uint32
	lastSequence uint16
	lost         bool
	building     bool
}

func NewVP8UnPacker() *VP8UnPacker {
	return &VP8UnPacker{
		frameBuffer: new(bytes.Buffer),
	}
}

func (unpacker *VP8UnPacker) UnPack(pkt []byte) error {
	pkg := &RtpPacket{}
	if err := pkg.Decode(pkt); err != nil {
		return err
	}

	if len(pkg.Payload) < 1 {
		return errors.New("vp8 rtp packet payload is empty")
	}

	if unpacker.onRtp != nil {
		unpacker.onRtp(pkg)
	}

	if !unpacker.building || pkg.Header.Timestamp != unpacker.timestamp {
		if unpacker.building && unpacker.frameBuffer.Len() > 0 {
			if unpacker.onFrame != nil {
				unpacker.onFrame(unpacker.frameBuffer.Bytes(), unpacker.timestamp, true)
			}
		}
		unpacker.building = true
		unpacker.timestamp = pkg.Header.Timestamp
		unpacker.lastSequence = pkg.Header.SequenceNumber
		unpacker.frameBuffer.Reset()
		unpacker.lost = false
	} else {
		if unpacker.lastSequence+1 != pkg.Header.SequenceNumber {
			unpacker.lost = true
		}
	}

	unpacker.lastSequence = pkg.Header.SequenceNumber

	payload := pkg.Payload
	headerLength := 1 // mandatory descriptor

	if (payload[0] & 0x80) > 0 { // X bit
		if len(payload) < 2 {
			return errors.New("invalid vp8 payload: short extended header")
		}
		extHdr := payload[1]
		headerLength++
		if (extHdr & 0x80) > 0 { // I bit
			if len(payload) < headerLength+1 {
				return errors.New("invalid vp8 payload: short picture id")
			}
			if (payload[headerLength] & 0x80) > 0 {
				headerLength += 2
			} else {
				headerLength += 1
			}
		}
		if (extHdr & 0x40) > 0 { // L bit
			headerLength++
		}
		if (extHdr&0x20) > 0 || (extHdr&0x10) > 0 { // T or K bit
			headerLength++
		}
	}

	if len(payload) < headerLength {
		unpacker.lost = true
	} else {
		unpacker.frameBuffer.Write(payload[headerLength:])
	}

	if pkg.Header.Marker == 1 {
		if unpacker.onFrame != nil {
			unpacker.onFrame(unpacker.frameBuffer.Bytes(), unpacker.timestamp, unpacker.lost)
		}
		unpacker.building = false
		unpacker.frameBuffer.Reset()
	}

	return nil
}
