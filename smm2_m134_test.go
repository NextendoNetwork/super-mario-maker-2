// Test de regresión: la respuesta de m=134 debe tener EXACTAMENTE 57 bytes y
// matchear byte por byte la captura medida de referencia, sino Ryujinx se cae.
package main

import (
	"bytes"
	"testing"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

// Measured reference body for m=134 RES (captura real, c=26/44/66, idénticas).
var refM134Body = []byte{
	0x00, 0x34, 0x00, 0x00, 0x00, // [u8 ver=0][u32 structBody=52]
	0x01, 0x00, 0x00, 0x00, // [u32 count=1]
	0x00, 0x27, 0x00, 0x00, 0x00, // [u8 ver=0][u32 elemBody=39]
	0x02, 0x00, 0x75, 0x00, // String("u")
	0x21, 0x00, 0x36, 0x39, 0x64, 0x33, 0x38, 0x66, 0x38, 0x31, 0x66, 0x62, 0x38, 0x64,
	0x32, 0x62, 0x39, 0x39, 0x37, 0x39, 0x61, 0x36, 0x34, 0x65, 0x34, 0x37, 0x66, 0x62, 0x63, 0x63,
	0x35, 0x35, 0x32, 0x34, 0x00, // String(hex MD5) + null
	0x3c, 0x00, 0x00, 0x00, // [u32 expiration=60]
}

func TestM134BodyMatchesReference(t *testing.T) {
	s := nex.NewSwitchSettings("nd1!e9A2kMsAbaqD", 40000) // access key irrelevant for raw bytes

	u := md5Hex("4If9rL9JRLMmEvD30GAxDl")
	if u != "69d38f81fb8d2b9979a64e47fbcc5524" {
		t.Fatalf("MD5 mismatch: got %q", u)
	}

	const kKey = "u"
	elemBody := uint32(2 + len(kKey) + 1 + 2 + len(u) + 1) // String(kKey) + String(u)
	structBody := uint32(4 + 5 + elemBody + 4)              // count + element substream header + element body + expiration

	out := nex.NewStreamOut(s)
	out.U8(0)
	out.U32(structBody)
	out.U32(1)
	out.U8(0)
	out.U32(elemBody)
	out.String(kKey)
	out.String(u)
	out.U32(60)

	got := out.Bytes()
	if len(got) != len(refM134Body) {
		t.Fatalf("body length mismatch: got %d, want %d\n  got:  % x\n  want: % x",
			len(got), len(refM134Body), got, refM134Body)
	}
	if !bytes.Equal(got, refM134Body) {
		t.Fatalf("body bytes mismatch:\n  got:  % x\n  want: % x", got, refM134Body)
	}
	t.Logf("OK: m=134 body is %d bytes, matches measured reference exactly", len(got))
}
