// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package filestore

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// transparent per-part zstd compression of db_file_data blobs.
// compression is fully internal to the stored blob: WaveFile.Size, part
// indices, and every offset computation stay on the uncompressed plaintext.
// the zstd frame is self-sizing on decode, so no length header is needed.

// the stateless EncodeAll/DecodeAll calls on these are safe for concurrent use,
// so a single shared encoder/decoder serves all filestore parts.
var zstdEncoder *zstd.Encoder
var zstdDecoder *zstd.Decoder

func init() {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		panic(fmt.Sprintf("filestore: cannot create zstd encoder: %v", err))
	}
	zstdEncoder = enc
	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("filestore: cannot create zstd decoder: %v", err))
	}
	zstdDecoder = dec
}

func compressPart(plain []byte) []byte {
	return zstdEncoder.EncodeAll(plain, nil)
}

func decompressPart(blob []byte) ([]byte, error) {
	return zstdDecoder.DecodeAll(blob, nil)
}
