package aead

import (
	"encoding/base64"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// Tink binary keyset support. KEYS.NEW_KEYSET in BigQuery returns a
// serialized google.crypto.tink.Keyset protocol buffer, and the
// GoogleSQL compliance fixtures (aead.test KeysetTable /
// DeterministicKeysetTable) store keysets in that form. loadKeyset
// accepts both the JSON form this package emits and the binary form,
// converting the latter into the in-memory keyset shape.
//
//	Keyset  { uint32 primary_key_id = 1; repeated Key key = 2; }
//	Key     { KeyData key_data = 1; KeyStatusType status = 2;
//	          uint32 key_id = 3; OutputPrefixType output_prefix_type = 4; }
//	KeyData { string type_url = 1; bytes value = 2; ... }
//	AesGcmKey { uint32 version = 1; ...; bytes key_value = 3; }
//	AesSivKey { uint32 version = 1; bytes key_value = 2; }
const (
	typeURLAESGCM    = "type.googleapis.com/google.crypto.tink.AesGcmKey"
	typeURLAESSIV    = "type.googleapis.com/google.crypto.tink.AesSivKey"
	keyStatusEnabled = 1
)

// forEachField walks the top-level fields of a serialized message.
func forEachField(b []byte, fn func(num protowire.Number, typ protowire.Type, v []byte, n uint64) error) error {
	for len(b) > 0 {
		num, typ, l := protowire.ConsumeTag(b)
		if l < 0 {
			return protowire.ParseError(l)
		}
		b = b[l:]
		var (
			raw []byte
			u   uint64
		)
		switch typ {
		case protowire.VarintType:
			u, l = protowire.ConsumeVarint(b)
		case protowire.BytesType:
			raw, l = protowire.ConsumeBytes(b)
		default:
			l = protowire.ConsumeFieldValue(num, typ, b)
		}
		if l < 0 {
			return protowire.ParseError(l)
		}
		b = b[l:]
		if err := fn(num, typ, raw, u); err != nil {
			return err
		}
	}
	return nil
}

func parseBinaryKeyset(b []byte) (*keyset, error) {
	var (
		primaryID uint64
		ks        keyset
	)
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, u uint64) error {
		switch {
		case num == 1 && typ == protowire.VarintType:
			primaryID = u
		case num == 2 && typ == protowire.BytesType:
			k, err := parseBinaryKey(v)
			if err != nil {
				return err
			}
			if k != nil {
				ks.Keys = append(ks.Keys, *k)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("keyset parse: %w", err)
	}
	for i := range ks.Keys {
		ks.Keys[i].Primary = uint64(ks.Keys[i].ID) == primaryID
	}
	return &ks, nil
}

func parseBinaryKey(b []byte) (*keysetKey, error) {
	var (
		keyData []byte
		status  uint64
		id      uint64
	)
	if err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, u uint64) error {
		switch num {
		case 1:
			keyData = v
		case 2:
			status = u
		case 3:
			id = u
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if status != keyStatusEnabled {
		return nil, nil
	}
	var (
		typeURL string
		value   []byte
	)
	if err := forEachField(keyData, func(num protowire.Number, typ protowire.Type, v []byte, u uint64) error {
		switch num {
		case 1:
			typeURL = string(v)
		case 2:
			value = v
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var (
		alg      string
		keyField protowire.Number
	)
	switch typeURL {
	case typeURLAESGCM:
		alg, keyField = algAESGCM, 3
	case typeURLAESSIV:
		alg, keyField = algAESSIVDET, 2
	default:
		return nil, fmt.Errorf("keyset parse: unsupported key type %q", typeURL)
	}
	var raw []byte
	if err := forEachField(value, func(num protowire.Number, typ protowire.Type, v []byte, u uint64) error {
		if num == keyField && typ == protowire.BytesType {
			raw = v
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("keyset parse: key has no key material")
	}
	return &keysetKey{
		ID:        uint32(id),
		Algorithm: alg,
		Key:       base64.StdEncoding.EncodeToString(raw),
	}, nil
}
