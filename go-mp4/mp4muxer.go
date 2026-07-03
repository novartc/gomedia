package mp4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

type MP4_FLAG uint32

// ffmpeg movenc.h
const (
	MP4_FLAG_FRAGMENT MP4_FLAG = (1 << 1)
	MP4_FLAG_KEYFRAME MP4_FLAG = (1 << 3)
	MP4_FLAG_CUSTOM   MP4_FLAG = (1 << 5)
	MP4_FLAG_DASH     MP4_FLAG = (1 << 11)
)

func (f MP4_FLAG) has(ff MP4_FLAG) bool {
	return (f & ff) != 0
}

func (f MP4_FLAG) isFragment() bool {
	return (f & MP4_FLAG_FRAGMENT) != 0
}

func (f MP4_FLAG) isDash() bool {
	return (f & MP4_FLAG_DASH) != 0
}

type OnFragment func(duration uint32, firstPts, firstDts uint64)
type Movmuxer struct {
	writer            io.WriteSeeker
	nextTrackId       uint32
	nextFragmentId    uint32
	mdatOffset        uint32
	tracks            map[uint32]*mp4track
	movFlag           MP4_FLAG
	onNewFragment     OnFragment
	fragDuration      uint32
	trackIdleTimeout  uint32
	moovReserveSize   uint32
	moovReserveOffset int64
	moovReserved      bool
	moovWritten       bool
}

type MuxerOption func(muxer *Movmuxer)

func WithMp4Flag(f MP4_FLAG) MuxerOption {
	return func(muxer *Movmuxer) {
		muxer.movFlag |= f
	}
}

func WithFragmentDuration(durationMs uint32) MuxerOption {
	return func(muxer *Movmuxer) {
		muxer.fragDuration = durationMs
		if muxer.moovReserveSize == 0 {
			muxer.moovReserveSize = 64 * 1024
		}
	}
}

func WithTrackIdleTimeout(timeoutMs uint32) MuxerOption {
	return func(muxer *Movmuxer) {
		muxer.trackIdleTimeout = timeoutMs
	}
}

func WithMoovReserveSize(size uint32) MuxerOption {
	return func(muxer *Movmuxer) {
		muxer.moovReserveSize = size
	}
}

func CreateMp4Muxer(w io.WriteSeeker, options ...MuxerOption) (*Movmuxer, error) {
	muxer := &Movmuxer{
		writer:         w,
		nextTrackId:    1,
		nextFragmentId: 1,
		tracks:         make(map[uint32]*mp4track),
		movFlag:        MP4_FLAG_KEYFRAME,
	}

	for _, opt := range options {
		opt(muxer)
	}

	if !muxer.movFlag.isFragment() && !muxer.movFlag.isDash() {
		ftyp := NewFileTypeBox()
		ftyp.Major_brand = mov_tag(isom)
		ftyp.Minor_version = 0x200
		ftyp.Compatible_brands = make([]uint32, 4)
		ftyp.Compatible_brands[0] = mov_tag(isom)
		ftyp.Compatible_brands[1] = mov_tag(iso2)
		ftyp.Compatible_brands[2] = mov_tag(avc1)
		ftyp.Compatible_brands[3] = mov_tag(mp41)
		length, boxdata := ftyp.Encode()
		_, err := muxer.writer.Write(boxdata[0:length])
		if err != nil {
			return nil, err
		}
		free := NewFreeBox()
		freelen, freeboxdata := free.Encode()
		_, err = muxer.writer.Write(freeboxdata[0:freelen])
		if err != nil {
			return nil, err
		}
		currentOffset, err := muxer.writer.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		muxer.mdatOffset = uint32(currentOffset)
		mdat := BasicBox{Type: [4]byte{'m', 'd', 'a', 't'}}
		mdat.Size = 8
		mdatlen, mdatBox := mdat.Encode()
		_, err = muxer.writer.Write(mdatBox[0:mdatlen])
		if err != nil {
			return nil, err
		}
	}
	return muxer, nil
}

type TrackOption func(track *mp4track)

func WithVideoWidth(width uint32) TrackOption {
	return func(track *mp4track) {
		track.width = width
	}
}

func WithVideoHeight(height uint32) TrackOption {
	return func(track *mp4track) {
		track.height = height
	}
}

func WithAudioChannelCount(channelCount uint8) TrackOption {
	return func(track *mp4track) {
		track.chanelCount = channelCount
	}
}

func WithAudioSampleRate(sampleRate uint32) TrackOption {
	return func(track *mp4track) {
		track.sampleRate = sampleRate
	}
}

func WithAudioSampleBits(sampleBits uint8) TrackOption {
	return func(track *mp4track) {
		track.sampleBits = sampleBits
	}
}

func WithExtraData(extraData []byte) TrackOption {
	return func(track *mp4track) {
		track.extraData = make([]byte, len(extraData))
		copy(track.extraData, extraData)
	}
}

func WithDelay(delay uint64) TrackOption {
	return func(track *mp4track) {
		track.elstDelay = delay
	}
}

func (muxer *Movmuxer) AddAudioTrack(cid MP4_CODEC_TYPE, options ...TrackOption) uint32 {
	return muxer.addTrack(cid, options...)
}

func (muxer *Movmuxer) AddVideoTrack(cid MP4_CODEC_TYPE, options ...TrackOption) uint32 {
	return muxer.addTrack(cid, options...)
}

func (muxer *Movmuxer) addTrack(cid MP4_CODEC_TYPE, options ...TrackOption) uint32 {
	var track *mp4track
	if muxer.movFlag.isDash() || muxer.movFlag.isFragment() {
		track = newmp4track(cid, newFmp4WriterSeeker(1024*1024))
	} else {
		track = newmp4track(cid, muxer.writer)
	}
	track.trackId = muxer.nextTrackId
	muxer.tracks[muxer.nextTrackId] = track
	muxer.nextTrackId++

	for _, opt := range options {
		opt(track)
	}

	return track.trackId
}

func (muxer *Movmuxer) Write(track uint32, data []byte, pts uint64, dts uint64) error {
	mp4track := muxer.tracks[track]
	err := mp4track.writeSample(data, pts, dts)
	if err != nil {
		return err
	}
	mp4track.hasInput = true
	mp4track.lastInputDts = dts
	if isVideo(mp4track.cid) {
		mp4track.active = true
	}

	if !muxer.movFlag.isFragment() && !muxer.movFlag.isDash() {
		return err
	}

	if muxer.fragDuration > 0 {
		if err := muxer.updateIdleTracks(mp4track, dts); err != nil {
			return err
		}
		return muxer.maybeFlushFragment(mp4track)
	}

	if isAudio(mp4track.cid) {
		return nil
	}

	// isCustion := muxer.movFlag.has(MP4_FLAG_CUSTOM)
	isKeyFrag := muxer.movFlag.has(MP4_FLAG_KEYFRAME)
	if isKeyFrag {
		if mp4track.lastSample.isKey && mp4track.duration > 0 {
			return muxer.flushFragmentWithCallback(mp4track)
		}
	}

	return nil
}

func (muxer *Movmuxer) updateIdleTracks(source *mp4track, dts uint64) error {
	if muxer.trackIdleTimeout == 0 || source == nil || !isAudio(source.cid) {
		return nil
	}

	for _, track := range muxer.tracks {
		if !isVideo(track.cid) || !track.active || !track.hasInput {
			continue
		}
		if dts < track.lastInputDts || dts-track.lastInputDts < uint64(muxer.trackIdleTimeout) {
			continue
		}
		if err := muxer.SetTrackActive(track.trackId, false); err != nil {
			return err
		}
	}
	return nil
}

func (muxer *Movmuxer) maybeFlushFragment(track *mp4track) error {
	if track == nil {
		return nil
	}

	if isAudio(track.cid) {
		if muxer.canFlushAudioOnly() && track.pendingDuration() >= muxer.fragDuration {
			return muxer.flushFragmentWithCallback(track)
		}
		return nil
	}

	if isVideo(track.cid) && track.lastSample.isKey && muxer.pendingDuration() >= muxer.fragDuration {
		return muxer.flushFragmentWithCallback(track)
	}
	return nil
}

func (muxer *Movmuxer) canFlushAudioOnly() bool {
	for _, track := range muxer.tracks {
		if !isVideo(track.cid) {
			continue
		}
		if len(track.samplelist) > 0 {
			return false
		}
		if track.lastSample != nil && track.lastSample.hasVcl {
			return false
		}
		if track.active && len(track.fragments) > 0 {
			return false
		}
	}
	return true
}

func (muxer *Movmuxer) pendingDuration() uint32 {
	duration := uint32(0)
	for _, track := range muxer.tracks {
		if d := track.pendingDuration(); d > duration {
			duration = d
		}
	}
	return duration
}

func (muxer *Movmuxer) flushFragmentWithCallback(track *mp4track) error {
	duration, firstPts, firstDts, ok := track.fragmentInfo()
	if err := muxer.flushFragment(); err != nil {
		return err
	}
	if ok && muxer.onNewFragment != nil {
		muxer.onNewFragment(duration, firstPts, firstDts)
	}
	return nil
}

func (muxer *Movmuxer) SetTrackActive(track uint32, active bool) error {
	if mp4track := muxer.tracks[track]; mp4track != nil {
		if !active {
			if err := mp4track.flush(); err != nil {
				return err
			}
			if (muxer.movFlag.isFragment() || muxer.movFlag.isDash()) && len(mp4track.samplelist) > 0 {
				if err := muxer.flushFragmentWithCallback(mp4track); err != nil {
					return err
				}
			}
		}
		mp4track.active = active
	}
	return nil
}

func (muxer *Movmuxer) WriteTrailer() (err error) {

	for _, track := range muxer.tracks {
		if err = track.flush(); err != nil {
			return
		}
	}

	switch {
	case muxer.movFlag.isDash():
	case muxer.movFlag.isFragment():
		var callbackTrack *mp4track
		for _, track := range muxer.tracks {
			if isVideo(track.cid) && len(track.samplelist) > 0 {
				callbackTrack = track
				break
			}
		}
		if callbackTrack == nil {
			for _, track := range muxer.tracks {
				if len(track.samplelist) > 0 {
					callbackTrack = track
					break
				}
			}
		}
		duration, firstPts, firstDts, ok := uint32(0), uint64(0), uint64(0), false
		if callbackTrack != nil {
			duration, firstPts, firstDts, ok = callbackTrack.fragmentInfo()
		}
		err = muxer.flushFragment()
		if err != nil {
			return err
		}
		if ok && muxer.onNewFragment != nil {
			muxer.onNewFragment(duration, firstPts, firstDts)
		}
		if err = muxer.writeReservedMoov(); err != nil {
			return err
		}
		return muxer.writeMfra()
	default:
		if err = muxer.reWriteMdatSize(); err != nil {
			return err
		}
		return muxer.writeMoov(muxer.writer)
	}
	return
}

func (muxer *Movmuxer) ReBindWriter(w io.WriteSeeker) {
	muxer.writer = w
}

func (muxer *Movmuxer) OnNewFragment(onFragment OnFragment) {
	muxer.onNewFragment = onFragment
}

func (muxer *Movmuxer) WriteInitSegment(w io.Writer) error {
	ftypBox := makeFtypBox(mov_tag(iso5), 0x200, []uint32{mov_tag(iso5), mov_tag(iso6), mov_tag(mp41)})
	_, err := w.Write(ftypBox)
	if err != nil {
		return err
	}
	return muxer.writeMoov(w)
}

func (muxer *Movmuxer) reserveMoovSpace() error {
	if muxer.moovReserved {
		return nil
	}
	if muxer.moovReserveSize < 8 {
		return errors.New("mp4: moov reserve size must be at least 8 bytes")
	}

	ftypBox := makeFtypBox(mov_tag(iso5), 0x200, []uint32{mov_tag(iso5), mov_tag(iso6), mov_tag(mp41)})
	if _, err := muxer.writer.Write(ftypBox); err != nil {
		return err
	}
	offset, err := muxer.writer.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	free := NewFreeBox()
	free.Data = make([]byte, int(muxer.moovReserveSize)-8)
	_, freeBox := free.Encode()
	if _, err = muxer.writer.Write(freeBox); err != nil {
		return err
	}
	muxer.moovReserveOffset = offset
	muxer.moovReserved = true
	return nil
}

func (muxer *Movmuxer) writeReservedMoov() error {
	if !muxer.movFlag.isFragment() || muxer.moovWritten {
		return nil
	}
	if !muxer.moovReserved {
		return nil
	}

	var moov bytes.Buffer
	if err := muxer.writeMoov(&moov); err != nil {
		return err
	}
	if moov.Len() > int(muxer.moovReserveSize) {
		return errors.New("mp4: reserved moov space is too small")
	}
	remaining := int(muxer.moovReserveSize) - moov.Len()
	if remaining > 0 && remaining < 8 {
		return errors.New("mp4: reserved moov space leaves invalid free box")
	}

	current, err := muxer.writer.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err = muxer.writer.Seek(muxer.moovReserveOffset, io.SeekStart); err != nil {
		return err
	}
	if _, err = muxer.writer.Write(moov.Bytes()); err != nil {
		return err
	}
	if remaining >= 8 {
		free := NewFreeBox()
		free.Data = make([]byte, remaining-8)
		_, freeBox := free.Encode()
		if _, err = muxer.writer.Write(freeBox); err != nil {
			return err
		}
	}
	if _, err = muxer.writer.Seek(current, io.SeekStart); err != nil {
		return err
	}
	muxer.moovWritten = true
	return nil
}

func (muxer *Movmuxer) reWriteMdatSize() (err error) {
	var currentOffset int64
	if currentOffset, err = muxer.writer.Seek(0, io.SeekCurrent); err != nil {
		return err
	}
	datalen := currentOffset - int64(muxer.mdatOffset)
	if datalen > 0xFFFFFFFF {
		mdat := BasicBox{Type: [4]byte{'m', 'd', 'a', 't'}}
		mdat.Size = uint64(datalen + 8)
		mdatBoxLen, mdatBox := mdat.Encode()
		if _, err = muxer.writer.Seek(int64(muxer.mdatOffset)-8, io.SeekStart); err != nil {
			return
		}
		if _, err = muxer.writer.Write(mdatBox[0:mdatBoxLen]); err != nil {
			return
		}
		if _, err = muxer.writer.Seek(currentOffset, io.SeekStart); err != nil {
			return
		}
	} else {
		if _, err = muxer.writer.Seek(int64(muxer.mdatOffset), io.SeekStart); err != nil {
			return
		}
		tmpdata := make([]byte, 4)
		binary.BigEndian.PutUint32(tmpdata, uint32(datalen))
		if _, err = muxer.writer.Write(tmpdata); err != nil {
			return
		}
		if _, err = muxer.writer.Seek(currentOffset, io.SeekStart); err != nil {
			return
		}
	}
	return
}

func (muxer *Movmuxer) writeMoov(w io.Writer) (err error) {
	var mvhd []byte
	var mvex []byte
	if muxer.movFlag.isDash() || muxer.movFlag.isFragment() {
		mvhd = makeMvhdBox(muxer.nextTrackId, 0)
		mvex = makeMvex(muxer)
	} else {
		maxdurtaion := uint32(0)
		for _, track := range muxer.tracks {
			if maxdurtaion < track.duration {
				maxdurtaion = track.duration
			}
		}
		mvhd = makeMvhdBox(muxer.nextTrackId, maxdurtaion)
	}
	moovsize := len(mvhd) + len(mvex)
	traks := make([][]byte, len(muxer.tracks))
	for i := uint32(1); i < muxer.nextTrackId; i++ {
		traks[i-1] = makeTrak(muxer.tracks[i], muxer.movFlag)
		moovsize += len(traks[i-1])
	}

	moov := BasicBox{Type: [4]byte{'m', 'o', 'o', 'v'}}
	moov.Size = 8 + uint64(moovsize)
	offset, moovBox := moov.Encode()
	copy(moovBox[offset:], mvhd)
	offset += len(mvhd)
	for _, trak := range traks {
		copy(moovBox[offset:], trak)
		offset += len(trak)
	}
	copy(moovBox[offset:], mvex)
	_, err = w.Write(moovBox)
	return
}

func (muxer *Movmuxer) writeMfra() (err error) {
	mfraSize := 0
	tfras := make([][]byte, len(muxer.tracks))
	for i := uint32(1); i < muxer.nextTrackId; i++ {
		tfras[i-1] = makeTfraBox(muxer.tracks[i])
		mfraSize += len(tfras[i-1])
	}

	mfro := makeMfroBox(uint32(mfraSize) + 24)
	mfraSize += len(mfro)
	mfra := BasicBox{Type: [4]byte{'m', 'f', 'r', 'a'}}
	mfra.Size = 8 + uint64(mfraSize)
	offset, mfraBox := mfra.Encode()
	for _, tfra := range tfras {
		copy(mfraBox[offset:], tfra)
		offset += len(tfra)
	}
	copy(mfraBox[offset:], mfro)
	_, err = muxer.writer.Write(mfraBox)
	return
}

func (muxer *Movmuxer) FlushFragment() (err error) {
	for _, track := range muxer.tracks {
		track.flush()
	}
	return muxer.flushFragment()
}

func (muxer *Movmuxer) flushFragment() (err error) {
	hasSamples := false
	for i := uint32(1); i < muxer.nextTrackId; i++ {
		if len(muxer.tracks[i].samplelist) > 0 {
			hasSamples = true
			break
		}
	}
	if !hasSamples {
		return nil
	}

	if muxer.movFlag.isFragment() {
		if muxer.nextFragmentId == 1 {
			if muxer.moovReserveSize > 0 {
				if err = muxer.reserveMoovSpace(); err != nil {
					return err
				}
			} else {
				ftypBox := makeFtypBox(mov_tag(iso5), 0x200, []uint32{mov_tag(iso5), mov_tag(iso6), mov_tag(mp41)})
				if _, err = muxer.writer.Write(ftypBox); err != nil {
					return err
				}
				if err = muxer.writeMoov(muxer.writer); err != nil {
					return err
				}
				muxer.moovWritten = true
			}
		}
	}

	var moofOffset int64
	if moofOffset, err = muxer.writer.Seek(0, io.SeekCurrent); err != nil {
		return err
	}
	var mdatlen uint64 = 0
	for i := uint32(1); i < muxer.nextTrackId; i++ {
		if len(muxer.tracks[i].samplelist) == 0 {
			continue
		}
		for j := 0; j < len(muxer.tracks[i].samplelist); j++ {
			muxer.tracks[i].samplelist[j].offset += mdatlen
		}
		ws := muxer.tracks[i].writer.(*fmp4WriterSeeker)
		mdatlen += uint64(len(ws.buffer))
	}
	mdatlen += 8

	moofSize := 0
	mfhd := makeMfhdBox(muxer.nextFragmentId)

	moofSize += len(mfhd)
	trafs := make([][]byte, len(muxer.tracks))
	for i := uint32(1); i < muxer.nextTrackId; i++ {
		if len(muxer.tracks[i].samplelist) == 0 {
			continue
		}
		traf := makeTraf(muxer.tracks[i], uint64(moofOffset), uint64(0))
		moofSize += len(traf)
		trafs[i-1] = traf
	}

	moofSize += 8 //moof box
	mfhd = makeMfhdBox(muxer.nextFragmentId)
	trafs = make([][]byte, len(muxer.tracks))
	for i := uint32(1); i < muxer.nextTrackId; i++ {
		if len(muxer.tracks[i].samplelist) == 0 {
			continue
		}
		traf := makeTraf(muxer.tracks[i], uint64(moofOffset), uint64(moofSize+8)) //moofSize + 8(mdat box)
		trafs[i-1] = traf
	}
	muxer.nextFragmentId++

	moof := BasicBox{Type: [4]byte{'m', 'o', 'o', 'f'}}
	moof.Size = uint64(moofSize)
	offset, moofBox := moof.Encode()
	copy(moofBox[offset:], mfhd)
	offset += len(mfhd)
	for i := range trafs {
		copy(moofBox[offset:], trafs[i])
		offset += len(trafs[i])
	}

	mdat := BasicBox{Type: [4]byte{'m', 'd', 'a', 't'}}
	mdat.Size = 8
	_, mdatBox := mdat.Encode()

	if muxer.movFlag.isDash() {
		stypBox := makeStypBox(mov_tag(msdh), 0, []uint32{mov_tag(msdh), mov_tag(msix)})
		_, err := muxer.writer.Write(stypBox)
		if err != nil {
			return err
		}

		for i := uint32(1); i < muxer.nextTrackId; i++ {
			sidx := makeSidxBox(muxer.tracks[i], 52*(muxer.nextTrackId-1-i), uint32(mdatlen)+uint32(len(moofBox))+52*(muxer.nextTrackId-i-1))
			_, err := muxer.writer.Write(sidx)
			if err != nil {
				return err
			}
		}
	}

	_, err = muxer.writer.Write(moofBox)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint32(mdatBox, uint32(mdatlen))
	_, err = muxer.writer.Write(mdatBox)
	if err != nil {
		return err
	}

	for i := uint32(1); i < muxer.nextTrackId; i++ {
		if len(muxer.tracks[i].samplelist) > 0 {
			firstPts := muxer.tracks[i].samplelist[0].pts
			firstDts := muxer.tracks[i].samplelist[0].dts
			lastPts := muxer.tracks[i].samplelist[len(muxer.tracks[i].samplelist)-1].pts
			lastDts := muxer.tracks[i].samplelist[len(muxer.tracks[i].samplelist)-1].dts
			frag := movFragment{
				offset:   uint64(moofOffset),
				duration: muxer.tracks[i].pendingDuration(),
				firstDts: firstDts,
				firstPts: firstPts,
				lastPts:  lastPts,
				lastDts:  lastDts,
			}
			muxer.tracks[i].fragments = append(muxer.tracks[i].fragments, frag)
		}
		ws := muxer.tracks[i].writer.(*fmp4WriterSeeker)
		_, err = muxer.writer.Write(ws.buffer)
		if err != nil {
			return err
		}
		ws.buffer = ws.buffer[:0]
		ws.offset = 0
		muxer.tracks[i].clearSamples()
	}
	return nil
}
