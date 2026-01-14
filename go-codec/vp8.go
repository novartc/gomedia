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

/*
Frame Tag, 3字节

0 1 2 3 4 5 6 7   8 9 10 11 12 13 14 15   16 17 18 19 20 21 22 23
+-+-+-+-+-+-+-+-+ +-+-+-+-+-+-+-+-+ +-+-+-+-+-+-+-+-+
|I|P|  Version  |     Partition 0 Length (19 bits)   |
+-+-+-+-+-+-+-+-+ +-+-+-+-+-+-+-+-+ +-+-+-+-+-+-+-+-+

I (1 bit): 关键帧标志。0 表示关键帧，1 表示非关键帧。
*/

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

/*
VP8 Frame:
├── Frame Tag (3 bytes)
│   ├── Key Frame? (I bit)
│   └── Partition 0 Length
├── [If Key Frame]
│   ├── Start Code (0x9D012A)
│   ├── Width, Height
├── Partition 0 (mode, control info)
├── Partition 1+ (macroblock residuals)
*/

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

func IsVP8KeyFrame(frame []byte) bool {
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

func GetVP8Resloution(frame []byte) (width int, height int, err error) {
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
	tag, err := DecodeFrameTag(keyframe)
	if err != nil {
		return nil, err
	}

	// vpcC box for VP8
	// Based on "VP Codec ISO Media File Format Binding"
	// The record is 8 bytes long without codecInitializationData
	vpcc := make([]byte, 8)

	// profile: from frame tag version
	vpcc[0] = byte(tag.Version)
	// level: undefined for VP8, use a default
	vpcc[1] = 0

	// Pack bitDepth, chromaSubsampling, and videoFullRangeFlag into one byte
	var bitDepth byte = 8           // VP8 is 8-bit
	var chromaSubsampling byte = 0  // 0 for 4:2:0
	var videoFullRangeFlag byte = 1 // VP8 is typically full range
	// bitDepth (4 bits), chromaSubsampling (3 bits), videoFullRangeFlag (1 bit)
	vpcc[2] = ((bitDepth & 0x0F) << 4) | ((chromaSubsampling & 0x07) << 1) | (videoFullRangeFlag & 0x01)

	// colourPrimaries: 2 for unspecified
	vpcc[3] = 2
	// transferCharacteristics: 2 for unspecified
	vpcc[4] = 2
	// matrixCoefficients: 2 for unspecified
	vpcc[5] = 2
	// codecInitializationDataSize: 0
	vpcc[6] = 0
	vpcc[7] = 0

	return vpcc, nil
}
