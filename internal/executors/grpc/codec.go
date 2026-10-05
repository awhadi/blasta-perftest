package grpc

import (
	"fmt"

	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/mem"
)

// rawBytes carries unparsed protobuf wire bytes through the gRPC codec without
// generated message types, so callers can load-test any unary method by
// supplying wire-format bytes for the request (and optionally the response).
type rawBytes []byte

func (r rawBytes) Marshal() ([]byte, error) {
	if r == nil {
		return []byte{}, nil
	}
	return []byte(r), nil
}

func (r *rawBytes) Unmarshal(data []byte) error {
	*r = append((*r)[:0], data...)
	return nil
}

func (r rawBytes) ProtoMessage() {}

// rawCodec marshals rawBytes and rejects everything else.
//
// This codec is attached per-call with grpc.ForceCodecV2 rather than registered
// globally as "proto". A global registration would replace the standard proto
// codec for the whole process, breaking any real protobuf traffic (including
// gRPC's own health and reflection services) that shares the binary.
type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) {
	switch m := v.(type) {
	case rawBytes:
		return m.Marshal()
	case *rawBytes:
		return m.Marshal()
	}
	return nil, fmt.Errorf("grpc rawCodec: cannot marshal %T", v)
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	r, ok := v.(*rawBytes)
	if !ok {
		return fmt.Errorf("grpc rawCodec: cannot unmarshal into %T", v)
	}
	return r.Unmarshal(data)
}

func (rawCodec) Name() string { return "proto" }

// codecV2 adapts the byte-slice codec to gRPC's buffer-slice interface so it can
// be passed to grpc.ForceCodecV2.
type codecV2 struct{}

func (codecV2) Marshal(v any) (mem.BufferSlice, error) {
	b, err := rawCodec{}.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	return mem.BufferSlice{mem.SliceBuffer(b)}, nil
}

func (codecV2) Unmarshal(data mem.BufferSlice, v any) error {
	r, ok := v.(*rawBytes)
	if !ok {
		return fmt.Errorf("grpc codecV2: cannot unmarshal into %T", v)
	}
	// Reader() materialises the buffers; the data is only valid during this
	// call, so copy it into rawBytes immediately.
	return r.Unmarshal(data.Materialize())
}

func (codecV2) Name() string { return rawCodec{}.Name() }

var _ encoding.CodecV2 = codecV2{}
