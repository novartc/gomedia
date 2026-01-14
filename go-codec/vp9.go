package codec

import (
	"errors"
)

/*
VP9 Frame:
┌──────────────────────────┐
│ Frame Marker (2 bits)    │
│ Profile (2 bits)         │
│ Show Frame / Keyframe    │
├──────────────────────────┤
│ Uncompressed Header      │
│  ├── Width / Height      │
│  ├── Color Space         │
│  ├── Loop Filter         │
│  └── Quant Params        │
├──────────────────────────┤
│ Compressed Header (熵编码)│
│  ├── Mode Info           │
│  ├── Motion Vectors      │
│  └── Token Partition     │
├──────────────────────────┤
│ Tile Data                │
│  ├── Tile 0              │
│  ├── Tile 1              │
│  └── Tile N              │
└──────────────────────────┘
*/

func IsVP9KeyFrame(frame []byte) bool {
	if len(frame) < 1 {
		return false
	}
	// The first byte of the uncompressed header:
	// bit 7,6: frame_marker (must be 10b)
	// bit 5,4: profile
	// bit 3: show_existing_frame
	// bit 2: frame_type (0 for keyframe)
	// bit 1: show_frame
	// bit 0: error_resilient_mode
	b := frame[0]
	if (b & 0xC0) != 0x80 { // the frame marker must be '10'
		return false
	}

	// If show_existing_frame is 1, the header structure is different, which
	// makes a simple check difficult. Most keyframes don't use this.
	// We'll assume show_existing_frame is 0 for this check.
	if (b & 0x08) != 0 { // show_existing_frame
		return false
	}

	frameType := (b >> 2) & 1
	return frameType == 0
}

func GetVP9Resloution(frame []byte) (width int, height int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("failed to parse VP9 header, likely due to out-of-bounds read")
		}
	}()

	bs := NewBitStream(frame)

	// --- Parse Uncompressed Header ---
	// See Section 6.2 of the VP9 Bitstream & Decoding Process Specification.
	bs.SkipBits(2) // Frame marker

	profileHigh := bs.GetBits(1)
	profileLow := bs.GetBits(1)
	profile := (profileHigh << 1) | profileLow
	if profile == 3 {
		bs.SkipBits(1) // extra profile bit
	}

	showExistingFrame := bs.GetBits(1)
	if showExistingFrame == 1 {
		bs.SkipBits(3) // frame_to_show_map_idx
	}

	bs.SkipBits(1) // frame_type (already checked to be 0)
	bs.SkipBits(1) // show_frame
	bs.SkipBits(1) // error_resilient_mode

	// --- Keyframe specific fields ---
	bs.SkipBits(24) // Sync code (0x49, 0x83, 0x42)

	// --- Color Config ---
	if profile >= 2 {
		highBitdepth := bs.GetBits(1)
		if highBitdepth == 1 {
			bs.SkipBits(1) // 12-bit if 1, 10-bit if 0
		}
	}

	colorSpace := bs.GetBits(3)

	if colorSpace != 7 { // 7 is CS_SRGB
		bs.SkipBits(1) // color_range
		if profile == 1 || profile == 3 {
			bs.SkipBits(2) // subsampling_x, subsampling_y
			bs.SkipBits(1) // reserved_zero
		}
	} else { // sRGB
		if profile == 1 || profile == 3 {
			bs.SkipBits(1) // reserved_zero
		}
	}

	// --- Frame Size ---
	renderAndFrameSizeDifferent := bs.GetBits(1)
	if renderAndFrameSizeDifferent == 1 {
		bs.SkipBits(16) // render_width_minus_1
		bs.SkipBits(16) // render_height_minus_1
	}

	widthMinus1 := bs.GetBits(16)
	heightMinus1 := bs.GetBits(16)

	width = int(widthMinus1) + 1
	height = int(heightMinus1) + 1

	return width, height, nil
}

// The vpcC box is defined in the "VP Codec ISO Media File Format Binding" specification.
// This implementation is based on version 1 of the specification for VP9.
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
func CreateVP9VpcCExtradata(keyframe []byte) ([]byte, error) {
	// Profile is in the first byte.
	b := keyframe[0]
	profile := (b >> 4) & 0x03
	// This is only correct if the profile is < 3. A full implementation
	// would need a bitstream parser to check the extra profile bit.
	// We proceed with this for simplicity, consistent with the vp8 implementation.

	// vpcC box for VP9
	// The record is 8 bytes long without codecInitializationData
	vpcc := make([]byte, 8)

	// profile
	vpcc[0] = profile
	// level: not present in vp9 bitstream, use a default value.
	// The spec suggests values like 10 for 1.0, etc. We use 0 as a generic default.
	vpcc[1] = 0
	// bitDepth
	var bitDepth byte = 8
	if profile == 2 || profile == 3 {
		// Profiles 2 and 3 are for 10/12 bit. We need to parse more to be sure.
		// Default to 10 for simplicity.
		bitDepth = 10
	}

	// chromaSubsampling: 0 for 4:2:0. Most common case.
	// Profiles 0 and 2 are 4:2:0 only.
	// Profiles 1 and 3 support more, but we default to 4:2:0.
	var chromaSubsampling byte = 0 // 4:2:0

	// videoFullRangeFlag: Default to studio range.
	var videoFullRangeFlag byte = 0

	// Pack bitDepth, chromaSubsampling, and videoFullRangeFlag into one byte
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
