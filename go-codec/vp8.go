package codec

import "errors"

type VP8FrameTag struct {
	FrameType     uint32 //0: I frame , 1: P frame
	Version       uint32
	Display       uint32
	FirstPartSize uint32
}

type VP8KeyFrameHead struct {
	Width      int
	Height     int
	HorizScale int
	VertScale  int
}

func DecodeFrameTag(frame []byte) (*VP8FrameTag, error) {
	if len(frame) < 3 {
		return nil, errors.New("frame bytes < 3")
	}
	var tmp uint32 = (uint32(frame[2]) << 16) | (uint32(frame[1]) << 8) | uint32(frame[0])
	tag := &VP8FrameTag{}
	tag.FrameType = tmp & 0x01
	tag.Version = (tmp >> 1) & 0x07
	tag.Display = (tmp >> 4) & 0x01
	tag.FirstPartSize = (tmp >> 5) & 0x7FFFF
	return tag, nil
}

func DecodeKeyFrameHead(frame []byte) (*VP8KeyFrameHead, error) {
	if len(frame) < 7 {
		return nil, errors.New("frame bytes < 3")
	}

	if frame[0] != 0x9d || frame[1] != 0x01 || frame[2] != 0x2a {
		return nil, errors.New("not find Start code")
	}

	head := &VP8KeyFrameHead{}
	head.Width = int(uint16(frame[4]&0x3f)<<8 | uint16(frame[3]))
	head.HorizScale = int(frame[4] >> 6)
	head.Height = int(uint16(frame[6]&0x3f)<<8 | uint16(frame[5]))
	head.VertScale = int(frame[6] >> 6)
	return head, nil
}

func IsKeyFrame(frame []byte) bool {
	tag, err := DecodeFrameTag(frame)
	if err != nil {
		return false
	}

	if tag.FrameType == 0 {
		return true
	} else {
		return false
	}
}

func GetResloution(frame []byte) (width int, height int, err error) {
	if !IsKeyFrame(frame) {
		return 0, 0, errors.New("the frame is not Key frame")
	}

	head, err := DecodeKeyFrameHead(frame[3:])
	if err != nil {
		return 0, 0, err
	}
	return head.Width, head.Height, nil
}

// The vpcC box is defined in the "VP Codec ISO Media File Format Binding" specification.
// For VP8, its usage is not as standardized as avcC for H.264. Some players/muxers
// may not require it and derive the information from the bitstream directly.
// This implementation is based on version 2.0 of the specification.
//
// aligned(8) class VPCodecConfigurationBox
//
//	extends FullBox('vpcC', version = 1, flags = 0)
//
//	{
//	  unsigned int(8)     profile;
//	  unsigned int(8)     level;
//	  unsigned int(8)     bitDepth;
//	  unsigned int(8)     chromaSubsampling;
//	  unsigned int(8)     videoFullRangeFlag;
//	  unsigned int(8)     colourPrimaries;
//	  unsigned int(8)     transferCharacteristics;
//	  unsigned int(8)     matrixCoefficients;
//	  unsigned int(16)    codecInitializationDataSize;
//	  unsigned int(8)     codecInitializationData[codecInitializationDataSize];
//	}
func CreateVP8VpcCExtradata(keyframe []byte) ([]byte, error) {
	if !IsKeyFrame(keyframe) {
		return nil, errors.New("not a keyframe")
	}

	tag, err := DecodeFrameTag(keyframe)
	if err != nil {
		return nil, err
	}

	// vpcC box for VP8
	// Based on "VP Codec ISO Media File Format Binding v2.0"
	// Total size: 4 (FullBox) + 8 (fields) + 2 (size) = 14 bytes
	vpcc := make([]byte, 14)

	// FullBox version and flags
	vpcc[0] = 1 // version
	vpcc[1] = 0 // flags
	vpcc[2] = 0 // flags
	vpcc[3] = 0 // flags

	// profile: from frame tag version
	vpcc[4] = byte(tag.Version)
	// level: undefined for VP8, use a default
	vpcc[5] = 0
	// bitDepth: VP8 is 8-bit
	vpcc[6] = 8
	// chromaSubsampling: 0 for 4:2:0
	vpcc[7] = 0
	// videoFullRangeFlag: VP8 is typically full range
	vpcc[8] = 1
	// colourPrimaries: 2 for unspecified
	vpcc[9] = 2
	// transferCharacteristics: 2 for unspecified
	vpcc[10] = 2
	// matrixCoefficients: 2 for unspecified
	vpcc[11] = 2
	// codecInitializationDataSize: 0
	vpcc[12] = 0
	vpcc[13] = 0

	return vpcc, nil
}
