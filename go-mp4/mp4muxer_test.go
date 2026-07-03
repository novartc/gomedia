package mp4

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"testing"

	"github.com/yapingcat/gomedia/go-codec"
	"github.com/yapingcat/gomedia/go-mpeg2"
)

func countMP4Boxes(data []byte, typ string) int {
	count := 0
	for offset := 0; offset+8 <= len(data); {
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		headerSize := uint64(8)
		if size == 1 {
			if offset+16 > len(data) {
				break
			}
			size = binary.BigEndian.Uint64(data[offset+8:])
			headerSize = 16
		} else if size == 0 {
			size = uint64(len(data) - offset)
		}
		if size < headerSize || offset+int(size) > len(data) {
			break
		}
		boxType := string(data[offset+4 : offset+8])
		if boxType == typ {
			count++
		}
		switch boxType {
		case "moov", "trak", "mdia", "minf", "stbl", "moof", "traf":
			count += countMP4Boxes(data[offset+int(headerSize):offset+int(size)], typ)
		}
		offset += int(size)
	}
	return count
}

func moofTrafCounts(data []byte) []int {
	counts := make([]int, 0)
	for offset := 0; offset+8 <= len(data); {
		size := uint64(binary.BigEndian.Uint32(data[offset:]))
		headerSize := uint64(8)
		if size == 1 {
			if offset+16 > len(data) {
				break
			}
			size = binary.BigEndian.Uint64(data[offset+8:])
			headerSize = 16
		} else if size == 0 {
			size = uint64(len(data) - offset)
		}
		if size < headerSize || offset+int(size) > len(data) {
			break
		}
		if string(data[offset+4:offset+8]) == "moof" {
			counts = append(counts, countMP4Boxes(data[offset+int(headerSize):offset+int(size)], "traf"))
		}
		offset += int(size)
	}
	return counts
}

func TestFragmentMuxerAudioOnlyWhileVideoDelayed(t *testing.T) {
	file, err := os.CreateTemp("", "gomedia-fragment-audio-only-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	muxer, err := CreateMp4Muxer(file, WithMp4Flag(MP4_FLAG_FRAGMENT), WithFragmentDuration(100))
	if err != nil {
		t.Fatal(err)
	}
	muxer.AddVideoTrack(MP4_CODEC_VP8, WithVideoWidth(640), WithVideoHeight(360))
	audioTrack := muxer.AddAudioTrack(
		MP4_CODEC_G711A,
		WithAudioChannelCount(1),
		WithAudioSampleRate(8000),
		WithAudioSampleBits(16),
	)

	for dts := uint64(0); dts <= 360; dts += 20 {
		if err := muxer.Write(audioTrack, []byte{0, 0, 0, 0}, dts, dts); err != nil {
			t.Fatal(err)
		}
	}
	if err := muxer.WriteTrailer(); err != nil {
		t.Fatal(err)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	moofCount := countMP4Boxes(data, "moof")
	trafCount := countMP4Boxes(data, "traf")
	if moofCount < 2 {
		t.Fatalf("expected multiple audio-only moof boxes, got %d", moofCount)
	}
	if trafCount != moofCount {
		t.Fatalf("expected one audio traf per audio-only moof, got moof=%d traf=%d", moofCount, trafCount)
	}
}

func TestFragmentMuxerAutoIdleVideoAllowsAudioOnly(t *testing.T) {
	file, err := os.CreateTemp("", "gomedia-fragment-video-idle-*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	muxer, err := CreateMp4Muxer(
		file,
		WithMp4Flag(MP4_FLAG_FRAGMENT),
		WithFragmentDuration(100),
		WithTrackIdleTimeout(120),
	)
	if err != nil {
		t.Fatal(err)
	}
	videoTrack := muxer.AddVideoTrack(
		MP4_CODEC_VP8,
		WithVideoWidth(640),
		WithVideoHeight(360),
		WithExtraData([]byte{0}),
	)
	audioTrack := muxer.AddAudioTrack(
		MP4_CODEC_G711A,
		WithAudioChannelCount(1),
		WithAudioSampleRate(8000),
		WithAudioSampleBits(16),
	)

	if err := muxer.Write(videoTrack, []byte{1, 0, 0, 0}, 0, 0); err != nil {
		t.Fatal(err)
	}
	for dts := uint64(0); dts <= 420; dts += 20 {
		if err := muxer.Write(audioTrack, []byte{0, 0, 0, 0}, dts, dts); err != nil {
			t.Fatal(err)
		}
	}
	if err := muxer.WriteTrailer(); err != nil {
		t.Fatal(err)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	trafCounts := moofTrafCounts(data)
	if len(trafCounts) < 3 {
		t.Fatalf("expected video+audio fragment followed by audio-only fragments, got %v", trafCounts)
	}
	if trafCounts[0] != 2 {
		t.Fatalf("expected first fragment to contain audio and video trafs, got %v", trafCounts)
	}
	hasAudioOnly := false
	for _, count := range trafCounts[1:] {
		if count == 1 {
			hasAudioOnly = true
			break
		}
	}
	if !hasAudioOnly {
		t.Fatalf("expected audio-only fragment after video idle, got %v", trafCounts)
	}
}

func TestCreateMp4Reader(t *testing.T) {
	f, err := os.Open("jellyfish-3-mbps-hd.h264.mp4")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()
	for err == nil {
		nn := int64(0)
		size := make([]byte, 4)
		_, err = io.ReadFull(f, size)
		if err != nil {
			break
		}
		nn += 4
		boxtype := make([]byte, 4)
		_, err = io.ReadFull(f, boxtype)
		if err != nil {
			break
		}
		nn += 4
		var isize uint64 = uint64(binary.BigEndian.Uint32(size))
		if isize == 1 {
			size := make([]byte, 8)
			_, err = io.ReadFull(f, size)
			if err != nil {
				break
			}
			isize = binary.BigEndian.Uint64(size)
			nn += 8
		}
		fmt.Printf("Read Box(%s) size:%d\n", boxtype, isize)
		f.Seek(int64(isize)-nn, 1)
	}
}

func TestCreateMp4Muxer(t *testing.T) {

	f, err := os.Open("jellyfish-3-mbps-hd.h265")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()

	mp4filename := "jellyfish-3-mbps-hd.h265.mp4"
	mp4file, err := os.OpenFile(mp4filename, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mp4file.Close()

	buf, _ := ioutil.ReadAll(f)
	pts := uint64(0)
	dts := uint64(0)
	ii := [3]uint64{33, 33, 34}
	idx := 0

	type args struct {
		wh io.WriteSeeker
	}
	tests := []struct {
		name string
		args args
		want *Movmuxer
	}{
		{name: "muxer h264", args: args{wh: mp4file}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			muxer, err := CreateMp4Muxer(tt.args.wh)
			if err != nil {
				fmt.Println(err)
				return
			}
			tid := muxer.AddVideoTrack(MP4_CODEC_H265)
			cache := make([]byte, 0)
			codec.SplitFrameWithStartCode(buf, func(nalu []byte) bool {
				ntype := codec.H265NaluType(nalu)
				if !codec.IsH265VCLNaluType(ntype) {
					cache = append(cache, nalu...)
					return true
				}
				if len(cache) > 0 {
					cache = append(cache, nalu...)
					muxer.Write(tid, cache, pts, dts)
					cache = cache[:0]
				} else {
					muxer.Write(tid, nalu, pts, dts)
				}
				pts += ii[idx]
				dts += ii[idx]
				idx++
				idx = idx % 3
				return true
			})
			fmt.Printf("last dts %d\n", dts)
			muxer.WriteTrailer()
		})
	}
}

func TestMuxAAC(t *testing.T) {
	f, err := os.Open("test.aac")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()

	mp4filename := "aac.mp4"
	mp4file, err := os.OpenFile(mp4filename, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mp4file.Close()

	aac, _ := ioutil.ReadAll(f)
	var pts uint64 = 0
	//var dts uint64 = 0
	//var i int = 0
	samples := uint64(0)
	muxer, err := CreateMp4Muxer(mp4file)
	if err != nil {
		fmt.Println(err)
		return
	}

	tid := muxer.AddAudioTrack(MP4_CODEC_AAC)
	codec.SplitAACFrame(aac, func(aac []byte) {
		samples += 1024
		pts = samples * 1000 / 44100
		// if i < 3 {
		// 	pts += 23
		// 	dts += 23
		// 	i++
		// } else {
		// 	pts += 24
		// 	dts += 24
		// 	i = 0
		// }
		muxer.Write(tid, aac, pts, pts)
		//fmt.Println(pts)
	})
	muxer.WriteTrailer()
}

func TestMuxMp4(t *testing.T) {
	tsfilename := `demo.ts` // input
	tsfile, err := os.Open(tsfilename)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer tsfile.Close()

	mp4filename := "test14.mp4" // output
	mp4file, err := os.OpenFile(mp4filename, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer mp4file.Close()

	muxer, err := CreateMp4Muxer(mp4file)
	if err != nil {
		fmt.Println(err)
		return
	}
	vtid := muxer.AddVideoTrack(MP4_CODEC_H264)
	atid := muxer.AddAudioTrack(MP4_CODEC_AAC)

	afile, err := os.OpenFile("r.aac", os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer afile.Close()
	demuxer := mpeg2.NewTSDemuxer()
	demuxer.OnFrame = func(cid mpeg2.TS_STREAM_TYPE, frame []byte, pts uint64, dts uint64) {

		if cid == mpeg2.TS_STREAM_AAC {
			err = muxer.Write(atid, frame, uint64(pts), uint64(dts))
			if err != nil {
				panic(err)
			}
		} else if cid == mpeg2.TS_STREAM_H264 {
			fmt.Println("pts,dts,len", pts, dts, len(frame))
			err = muxer.Write(vtid, frame, uint64(pts), uint64(dts))
			if err != nil {
				panic(err)
			}
		} else {
			panic("unkwon cid " + strconv.Itoa(int(cid)))
		}
	}

	err = demuxer.Input(tsfile)
	if err != nil {
		panic(err)
	}

	err = muxer.WriteTrailer()
	if err != nil {
		panic(err)
	}
}
