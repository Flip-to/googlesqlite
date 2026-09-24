// Package aead implements the BigQuery AEAD encryption functions
// (AEAD.ENCRYPT, AEAD.DECRYPT_*, DETERMINISTIC_ENCRYPT, KEYS.*).
//
// Keysets use the same representation as BigQuery: a serialized Tink
// `google.crypto.tink.Keyset` protocol buffer, so keysets created by
// BigQuery can be used here and vice versa, and KEYS.KEYSET_TO_JSON /
// KEYS.KEYSET_FROM_JSON convert to and from Tink's JSON keyset format.
//
//   - AEAD_AES_GCM_256 keys are AesGcmKey protos. Ciphertext is
//     prefix || 12-byte IV || AES-GCM ciphertext || 16-byte tag, where
//     prefix is 0x01 || 4-byte big-endian key id for TINK keys and
//     empty for RAW keys (keys added with KEYS.ADD_KEY_FROM_RAW_BYTES).
//   - AES_CBC_PKCS keys (AesCbcPkcs7Key) only decrypt: the ciphertext
//     is a 16-byte IV followed by AES-CBC with PKCS#7 padding.
//   - DETERMINISTIC_AEAD_AES_SIV_CMAC_256 keys are 64-byte AesSivKey
//     protos; ciphertext is prefix || AES-SIV (RFC 5297) output.
package aead

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-json"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

const (
	algAESGCM    = "AEAD_AES_GCM_256"
	algAESSIVDET = "DETERMINISTIC_AEAD_AES_SIV_CMAC_256"

	typeURLAESGCM     = "type.googleapis.com/google.crypto.tink.AesGcmKey"
	typeURLAESSIV     = "type.googleapis.com/google.crypto.tink.AesSivKey"
	typeURLAESCBCPKCS = "type.googleapis.com/google.crypto.tink.AesCbcPkcs7Key"

	gcmNonceSize   = 12
	cbcIVSize      = 16
	tinkPrefixSize = 5
	tinkStartByte  = 0x01
)

// Tink enum values.
const (
	statusEnabled   = 1
	prefixTink      = 1
	prefixRaw       = 3
	materialSymmetr = 1
)

var (
	statusNames       = map[int64]string{0: "UNKNOWN_STATUS", 1: "ENABLED", 2: "DISABLED", 3: "DESTROYED"}
	prefixNames       = map[int64]string{0: "UNKNOWN_PREFIX", 1: "TINK", 2: "LEGACY", 3: "RAW", 4: "CRUNCHY", 5: "WITH_ID_REQUIREMENT"}
	materialTypeNames = map[int64]string{0: "UNKNOWN_KEYMATERIAL", 1: "SYMMETRIC", 2: "ASYMMETRIC_PRIVATE", 3: "ASYMMETRIC_PUBLIC", 4: "REMOTE"}
)

// keyData mirrors google.crypto.tink.KeyData.
type keyData struct {
	TypeURL         string
	Value           []byte
	KeyMaterialType int64
}

// keysetKey mirrors google.crypto.tink.Keyset.Key.
type keysetKey struct {
	Data             *keyData
	Status           int64
	KeyID            uint32
	OutputPrefixType int64
}

// keyset mirrors google.crypto.tink.Keyset.
type keyset struct {
	PrimaryKeyID uint32
	Keys         []keysetKey
}

// --- protobuf encoding ---

func (ks *keyset) marshal() []byte {
	var b []byte
	if ks.PrimaryKeyID != 0 {
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(ks.PrimaryKeyID))
	}
	for i := range ks.Keys {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendBytes(b, ks.Keys[i].marshal())
	}
	return b
}

func (k *keysetKey) marshal() []byte {
	var b []byte
	if k.Data != nil {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, k.Data.marshal())
	}
	if k.Status != 0 {
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(k.Status))
	}
	if k.KeyID != 0 {
		b = protowire.AppendTag(b, 3, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(k.KeyID))
	}
	if k.OutputPrefixType != 0 {
		b = protowire.AppendTag(b, 4, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(k.OutputPrefixType))
	}
	return b
}

func (d *keyData) marshal() []byte {
	var b []byte
	if d.TypeURL != "" {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendString(b, d.TypeURL)
	}
	if len(d.Value) != 0 {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendBytes(b, d.Value)
	}
	if d.KeyMaterialType != 0 {
		b = protowire.AppendTag(b, 3, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(d.KeyMaterialType))
	}
	return b
}

var errInvalidKeyset = errors.New("Invalid keyset") //nolint:staticcheck // BigQuery's error text

// walkMessage calls fn for every field of a serialized message. fn
// receives either a varint (for VarintType) or the field bytes (for
// BytesType); other wire types are skipped.
func walkMessage(b []byte, fn func(num protowire.Number, typ protowire.Type, v uint64, data []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return errInvalidKeyset
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return errInvalidKeyset
			}
			b = b[n:]
			if err := fn(num, typ, v, nil); err != nil {
				return err
			}
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return errInvalidKeyset
			}
			b = b[n:]
			if err := fn(num, typ, 0, v); err != nil {
				return err
			}
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return errInvalidKeyset
			}
			b = b[n:]
		}
	}
	return nil
}

func unmarshalKeyset(b []byte) (*keyset, error) {
	ks := &keyset{}
	err := walkMessage(b, func(num protowire.Number, typ protowire.Type, v uint64, data []byte) error {
		switch {
		case num == 1 && typ == protowire.VarintType:
			ks.PrimaryKeyID = uint32(v)
		case num == 2 && typ == protowire.BytesType:
			k, err := unmarshalKey(data)
			if err != nil {
				return err
			}
			ks.Keys = append(ks.Keys, *k)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ks, nil
}

func unmarshalKey(b []byte) (*keysetKey, error) {
	k := &keysetKey{}
	err := walkMessage(b, func(num protowire.Number, typ protowire.Type, v uint64, data []byte) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			d := &keyData{}
			if err := walkMessage(data, func(num protowire.Number, typ protowire.Type, v uint64, data []byte) error {
				switch {
				case num == 1 && typ == protowire.BytesType:
					d.TypeURL = string(data)
				case num == 2 && typ == protowire.BytesType:
					d.Value = append([]byte(nil), data...)
				case num == 3 && typ == protowire.VarintType:
					d.KeyMaterialType = int64(v)
				}
				return nil
			}); err != nil {
				return err
			}
			k.Data = d
		case num == 2 && typ == protowire.VarintType:
			k.Status = int64(v)
		case num == 3 && typ == protowire.VarintType:
			k.KeyID = uint32(v)
		case num == 4 && typ == protowire.VarintType:
			k.OutputPrefixType = int64(v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return k, nil
}

// keyValue extracts the raw key material from an AesGcmKey /
// AesCbcPkcs7Key (field 3) or AesSivKey (field 2) proto.
func keyValue(d *keyData) ([]byte, error) {
	field := protowire.Number(3)
	if d.TypeURL == typeURLAESSIV {
		field = 2
	}
	var out []byte
	err := walkMessage(d.Value, func(num protowire.Number, typ protowire.Type, _ uint64, data []byte) error {
		if num == field && typ == protowire.BytesType {
			out = append([]byte(nil), data...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errInvalidKeyset
	}
	return out, nil
}

func keyProto(typeURL string, raw []byte) []byte {
	field := protowire.Number(3)
	if typeURL == typeURLAESSIV {
		field = 2
	}
	b := protowire.AppendTag(nil, field, protowire.BytesType)
	return protowire.AppendBytes(b, raw)
}

// --- keyset helpers ---

// loadKeyset parses a serialized keyset and checks that it is usable:
// at least one key, and a primary key id that names one of them.
func loadKeyset(b []byte) (*keyset, error) {
	if len(b) == 0 {
		return nil, errors.New("Invalid keyset: keyset is empty") //nolint:staticcheck // BigQuery's error text
	}
	ks, err := unmarshalKeyset(b)
	if err != nil {
		return nil, err
	}
	if len(ks.Keys) == 0 {
		return nil, errors.New("Invalid keyset: keyset has no keys") //nolint:staticcheck // BigQuery's error text
	}
	if ks.primary() == nil {
		return nil, errors.New("Invalid keyset: keyset has no primary key") //nolint:staticcheck // BigQuery's error text
	}
	return ks, nil
}

func (ks *keyset) primary() *keysetKey {
	for i := range ks.Keys {
		if ks.Keys[i].KeyID == ks.PrimaryKeyID && ks.Keys[i].Status == statusEnabled {
			return &ks.Keys[i]
		}
	}
	return nil
}

// newKeyID returns a random key id that is not yet used in ks.
func (ks *keyset) newKeyID() (uint32, error) {
	for {
		var buf [4]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return 0, err
		}
		id := binary.BigEndian.Uint32(buf[:])
		if id == 0 {
			continue
		}
		used := false
		for i := range ks.Keys {
			if ks.Keys[i].KeyID == id {
				used = true
				break
			}
		}
		if !used {
			return id, nil
		}
	}
}

// addGeneratedKey appends a fresh key of the given key type (as named
// by KEYS.NEW_KEYSET) and makes it primary.
func (ks *keyset) addGeneratedKey(keyType string) error {
	var typeURL string
	var size int
	switch keyType {
	case algAESGCM:
		typeURL, size = typeURLAESGCM, 32
	case algAESSIVDET:
		typeURL, size = typeURLAESSIV, 64
	default:
		return fmt.Errorf("Unsupported key type: %s", keyType) //nolint:staticcheck // BigQuery's error text
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	id, err := ks.newKeyID()
	if err != nil {
		return err
	}
	ks.Keys = append(ks.Keys, keysetKey{
		Data:             &keyData{TypeURL: typeURL, Value: keyProto(typeURL, raw), KeyMaterialType: materialSymmetr},
		Status:           statusEnabled,
		KeyID:            id,
		OutputPrefixType: prefixTink,
	})
	ks.PrimaryKeyID = id
	return nil
}

// keysetFromArg extracts a keyset BYTES payload from a function
// argument. STRUCT-shaped keysets (the KEYS.KEYSET_CHAIN form) carry
// the resolved payload in a `keyset` field; we accept that path too.
func keysetFromArg(v value.Value) ([]byte, error) {
	if v == nil {
		return nil, errors.New("keyset is NULL")
	}
	switch x := v.(type) {
	case value.BytesValue:
		return []byte(x), nil
	case value.StringValue:
		return []byte(string(x)), nil
	case *value.StructValue:
		for i, k := range x.Keys {
			if k == "keyset" && i < len(x.Values) && x.Values[i] != nil {
				return keysetFromArg(x.Values[i])
			}
		}
		return nil, errors.New("keyset STRUCT has no `keyset` field")
	}
	s, err := v.ToString()
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

func plaintextFromArg(v value.Value) ([]byte, error) {
	switch x := v.(type) {
	case value.BytesValue:
		return []byte(x), nil
	}
	s, err := v.ToString()
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// --- KEYS.* ---

// BindKeysNewKeyset returns a fresh keyset with one key of the
// requested type.
func BindKeysNewKeyset(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("KEYS.NEW_KEYSET: invalid number of arguments: got %d, want 1", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	keyType, err := args[0].ToString()
	if err != nil {
		return nil, err
	}
	ks := &keyset{}
	if err := ks.addGeneratedKey(keyType); err != nil {
		return nil, fmt.Errorf("KEYS.NEW_KEYSET: %w", err)
	}
	return value.BytesValue(ks.marshal()), nil
}

// BindKeysAddKeyFromRawBytes appends externally-provided key material
// as a RAW, non-primary key.
func BindKeysAddKeyFromRawBytes(args ...value.Value) (value.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("KEYS.ADD_KEY_FROM_RAW_BYTES: invalid number of arguments: got %d, want 3", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	ksBytes, err := args[0].ToBytes()
	if err != nil {
		return nil, err
	}
	keyType, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	raw, err := args[2].ToBytes()
	if err != nil {
		return nil, err
	}
	var typeURL string
	switch keyType {
	case "AES_GCM":
		typeURL = typeURLAESGCM
		if len(raw) != 16 && len(raw) != 32 {
			return nil, fmt.Errorf("Failed to add a key from raw bytes: Unsupported key size: %d bytes; expected 16 or 32 bytes.", len(raw)) //nolint:staticcheck // BigQuery's error text
		}
	case "AES_CBC_PKCS":
		typeURL = typeURLAESCBCPKCS
		if len(raw) != 16 && len(raw) != 24 && len(raw) != 32 {
			return nil, fmt.Errorf("Failed to add a key from raw bytes: Unsupported key size: %d bytes; expected 16, 24, or 32 bytes.", len(raw)) //nolint:staticcheck // BigQuery's error text
		}
	default:
		return nil, fmt.Errorf("Invalid key type provided to KEYS.ADD_KEY_FROM_RAW_BYTES: %s", keyType) //nolint:staticcheck // BigQuery's error text
	}
	ks, err := loadKeyset(ksBytes)
	if err != nil {
		return nil, err
	}
	id, err := ks.newKeyID()
	if err != nil {
		return nil, err
	}
	ks.Keys = append(ks.Keys, keysetKey{
		Data:             &keyData{TypeURL: typeURL, Value: keyProto(typeURL, raw), KeyMaterialType: materialSymmetr},
		Status:           statusEnabled,
		KeyID:            id,
		OutputPrefixType: prefixRaw,
	})
	return value.BytesValue(ks.marshal()), nil
}

// BindKeysKeysetLength returns the number of keys in the keyset.
func BindKeysKeysetLength(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("KEYS.KEYSET_LENGTH: invalid number of arguments: got %d, want 1", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	b, err := keysetFromArg(args[0])
	if err != nil {
		return nil, err
	}
	ks, err := loadKeyset(b)
	if err != nil {
		return nil, err
	}
	return value.IntValue(int64(len(ks.Keys))), nil
}

// BindKeysKeysetToJson renders the keyset in Tink's JSON keyset format.
func BindKeysKeysetToJson(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("KEYS.KEYSET_TO_JSON: invalid number of arguments: got %d, want 1", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	b, err := keysetFromArg(args[0])
	if err != nil {
		return nil, err
	}
	ks, err := unmarshalKeyset(b)
	if err != nil {
		return nil, err
	}
	return value.StringValue(keysetJSON(ks)), nil
}

// keysetJSON prints a keyset the way protobuf's JSON printer does for
// google.crypto.tink.Keyset: lowerCamelCase field names in field-name
// order, default values omitted, enums by name, bytes as base64.
func keysetJSON(ks *keyset) string {
	var sb strings.Builder
	sb.WriteByte('{')
	first := true
	field := func(name string) {
		if !first {
			sb.WriteByte(',')
		}
		first = false
		sb.WriteString(`"` + name + `":`)
	}
	if len(ks.Keys) > 0 {
		field("key")
		sb.WriteByte('[')
		for i := range ks.Keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeKeyJSON(&sb, &ks.Keys[i])
		}
		sb.WriteByte(']')
	}
	if ks.PrimaryKeyID != 0 {
		field("primaryKeyId")
		fmt.Fprintf(&sb, "%d", ks.PrimaryKeyID)
	}
	sb.WriteByte('}')
	return sb.String()
}

func writeKeyJSON(sb *strings.Builder, k *keysetKey) {
	var parts []string
	if k.Data != nil {
		var d []string
		if k.Data.KeyMaterialType != 0 {
			d = append(d, `"keyMaterialType":`+enumJSON(materialTypeNames, k.Data.KeyMaterialType))
		}
		if k.Data.TypeURL != "" {
			s, _ := json.Marshal(k.Data.TypeURL)
			d = append(d, `"typeUrl":`+string(s))
		}
		if len(k.Data.Value) != 0 {
			d = append(d, `"value":"`+base64.StdEncoding.EncodeToString(k.Data.Value)+`"`)
		}
		parts = append(parts, `"keyData":{`+strings.Join(d, ",")+`}`)
	}
	if k.KeyID != 0 {
		parts = append(parts, fmt.Sprintf(`"keyId":%d`, k.KeyID))
	}
	if k.OutputPrefixType != 0 {
		parts = append(parts, `"outputPrefixType":`+enumJSON(prefixNames, k.OutputPrefixType))
	}
	if k.Status != 0 {
		parts = append(parts, `"status":`+enumJSON(statusNames, k.Status))
	}
	sb.WriteString("{" + strings.Join(parts, ",") + "}")
}

func enumJSON(names map[int64]string, v int64) string {
	if n, ok := names[v]; ok {
		return `"` + n + `"`
	}
	return fmt.Sprintf("%d", v)
}

func enumFromJSON(names map[int64]string, raw json.RawMessage) (int64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		for k, n := range names {
			if n == s {
				return k, nil
			}
		}
		return 0, fmt.Errorf("Invalid keyset JSON: unknown enum value %q", s) //nolint:staticcheck // BigQuery's error text
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("Invalid keyset JSON: %w", err) //nolint:staticcheck // BigQuery's error text
	}
	return n, nil
}

// BindKeysKeysetFromJson parses a Tink JSON keyset into its serialized
// protocol buffer form.
func BindKeysKeysetFromJson(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("KEYS.KEYSET_FROM_JSON: invalid number of arguments: got %d, want 1", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	s, err := args[0].ToString()
	if err != nil {
		return nil, err
	}
	ks, err := parseKeysetJSON([]byte(s))
	if err != nil {
		return nil, err
	}
	return value.BytesValue(ks.marshal()), nil
}

func parseKeysetJSON(b []byte) (*keyset, error) {
	var top struct {
		Key          []map[string]json.RawMessage `json:"key"`
		PrimaryKeyID *uint32                      `json:"primaryKeyId"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&top); err != nil {
		return nil, fmt.Errorf("Invalid keyset JSON: %w", err) //nolint:staticcheck // BigQuery's error text
	}
	ks := &keyset{}
	if top.PrimaryKeyID != nil {
		ks.PrimaryKeyID = *top.PrimaryKeyID
	}
	for _, m := range top.Key {
		var k keysetKey
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			raw := m[name]
			var err error
			switch name {
			case "keyData":
				var d struct {
					TypeURL         string          `json:"typeUrl"`
					Value           []byte          `json:"value"`
					KeyMaterialType json.RawMessage `json:"keyMaterialType"`
				}
				if err = json.Unmarshal(raw, &d); err != nil {
					return nil, fmt.Errorf("Invalid keyset JSON: %w", err) //nolint:staticcheck // BigQuery's error text
				}
				k.Data = &keyData{TypeURL: d.TypeURL, Value: d.Value}
				if len(d.KeyMaterialType) > 0 {
					if k.Data.KeyMaterialType, err = enumFromJSON(materialTypeNames, d.KeyMaterialType); err != nil {
						return nil, err
					}
				}
			case "keyId":
				var id uint32
				if err = json.Unmarshal(raw, &id); err != nil {
					return nil, fmt.Errorf("Invalid keyset JSON: %w", err) //nolint:staticcheck // BigQuery's error text
				}
				k.KeyID = id
			case "status":
				if k.Status, err = enumFromJSON(statusNames, raw); err != nil {
					return nil, err
				}
			case "outputPrefixType":
				if k.OutputPrefixType, err = enumFromJSON(prefixNames, raw); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("Invalid keyset JSON: unknown field %q", name) //nolint:staticcheck // BigQuery's error text
			}
		}
		ks.Keys = append(ks.Keys, k)
	}
	return ks, nil
}

// BindKeysRotateKeyset adds a fresh primary key to the keyset. An
// empty keyset yields a new keyset with a single key.
func BindKeysRotateKeyset(args ...value.Value) (value.Value, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("KEYS.ROTATE_KEYSET: missing argument")
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	b, err := keysetFromArg(args[0])
	if err != nil {
		return nil, err
	}
	ks := &keyset{}
	if len(b) > 0 {
		if ks, err = loadKeyset(b); err != nil {
			return nil, err
		}
	}
	keyType := algAESGCM
	if len(args) >= 2 {
		if keyType, err = args[1].ToString(); err != nil {
			return nil, err
		}
	}
	if err := ks.addGeneratedKey(keyType); err != nil {
		return nil, fmt.Errorf("KEYS.ROTATE_KEYSET: %w", err)
	}
	return value.BytesValue(ks.marshal()), nil
}

// BindKeysKeysetChain resolves a wrapped-keyset chain into a STRUCT
// the AEAD.* functions can accept. Without Cloud KMS we treat the
// wrapped material as a passthrough.
func BindKeysKeysetChain(args ...value.Value) (value.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("KEYS.KEYSET_CHAIN: missing argument")
	}
	if helper.ExistsNull(args[:2]) {
		return nil, nil
	}
	wrapped, err := args[1].ToBytes()
	if err != nil {
		return nil, err
	}
	return &value.StructValue{
		Keys:   []string{"keyset"},
		Values: []value.Value{value.BytesValue(wrapped)},
	}, nil
}

// BindKeysNewWrappedKeyset is a stub: without a KMS wrapper we return
// an unwrapped keyset.
func BindKeysNewWrappedKeyset(args ...value.Value) (value.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("KEYS.NEW_WRAPPED_KEYSET: missing argument")
	}
	if helper.ExistsNull(args[:2]) {
		return nil, nil
	}
	keyType, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	return BindKeysNewKeyset(value.StringValue(keyType))
}

// BindKeysRewrapKeyset is similarly a no-op without a KMS layer.
func BindKeysRewrapKeyset(args ...value.Value) (value.Value, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("KEYS.REWRAP_KEYSET: missing argument")
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	return args[2], nil
}

// BindKeysRotateWrappedKeyset is similarly a passthrough rotation.
func BindKeysRotateWrappedKeyset(args ...value.Value) (value.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("KEYS.ROTATE_WRAPPED_KEYSET: missing argument")
	}
	if helper.ExistsNull(args[:2]) {
		return nil, nil
	}
	return args[1], nil
}

// --- encryption ---

func outputPrefix(k *keysetKey) []byte {
	if k.OutputPrefixType == prefixRaw {
		return nil
	}
	p := make([]byte, tinkPrefixSize)
	p[0] = tinkStartByte
	binary.BigEndian.PutUint32(p[1:], k.KeyID)
	return p
}

func loadArgs(name string, args []value.Value) (*keyset, []byte, []byte, error) {
	ksBytes, err := keysetFromArg(args[0])
	if err != nil {
		return nil, nil, nil, err
	}
	ks, err := loadKeyset(ksBytes)
	if err != nil {
		return nil, nil, nil, err
	}
	data, err := plaintextFromArg(args[1])
	if err != nil {
		return nil, nil, nil, err
	}
	aad, err := plaintextFromArg(args[2])
	if err != nil {
		return nil, nil, nil, err
	}
	return ks, data, aad, nil
}

// BindAeadEncrypt implements AEAD.ENCRYPT.
func BindAeadEncrypt(args ...value.Value) (value.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("AEAD.ENCRYPT: invalid number of arguments: got %d, want 3", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	ks, plaintext, aad, err := loadArgs("AEAD.ENCRYPT", args)
	if err != nil {
		return nil, err
	}
	primary := ks.primary()
	if primary.Data == nil || primary.Data.TypeURL != typeURLAESGCM {
		return nil, fmt.Errorf("AEAD.ENCRYPT: the primary key of the keyset is not an %s key", algAESGCM)
	}
	key, err := keyValue(primary.Data)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(outputPrefix(primary), nonce...)
	out = gcm.Seal(out, nonce, plaintext, aad)
	return value.BytesValue(out), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// BindDeterministicEncrypt implements DETERMINISTIC_ENCRYPT with
// AES-SIV.
func BindDeterministicEncrypt(args ...value.Value) (value.Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("DETERMINISTIC_ENCRYPT: invalid number of arguments: got %d, want 3", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	ks, plaintext, aad, err := loadArgs("DETERMINISTIC_ENCRYPT", args)
	if err != nil {
		return nil, err
	}
	primary := ks.primary()
	if primary.Data == nil || primary.Data.TypeURL != typeURLAESSIV {
		return nil, fmt.Errorf("DETERMINISTIC_ENCRYPT: the primary key of the keyset is not a %s key", algAESSIVDET)
	}
	key, err := keyValue(primary.Data)
	if err != nil {
		return nil, err
	}
	ct, err := sivEncrypt(key, plaintext, aad)
	if err != nil {
		return nil, err
	}
	return value.BytesValue(append(outputPrefix(primary), ct...)), nil
}

// decryptCommon factors the shared AEAD.DECRYPT_BYTES /
// AEAD.DECRYPT_STRING / DETERMINISTIC_DECRYPT_* flow: keys whose TINK
// prefix matches the ciphertext are tried first, then RAW keys.
func decryptCommon(name string, deterministic bool, args []value.Value) ([]byte, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s: invalid number of arguments: got %d, want 3", name, len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	ks, aad, err := func() (*keyset, []byte, error) {
		ksBytes, err := keysetFromArg(args[0])
		if err != nil {
			return nil, nil, err
		}
		ks, err := loadKeyset(ksBytes)
		if err != nil {
			return nil, nil, err
		}
		aad, err := plaintextFromArg(args[2])
		return ks, aad, err
	}()
	if err != nil {
		return nil, err
	}
	ct, err := args[1].ToBytes()
	if err != nil {
		return nil, err
	}
	var candidates []*keysetKey
	for i := range ks.Keys {
		k := &ks.Keys[i]
		if k.Status != statusEnabled || k.Data == nil || k.OutputPrefixType == prefixRaw {
			continue
		}
		if p := outputPrefix(k); len(ct) >= len(p) && bytes.Equal(ct[:len(p)], p) {
			candidates = append(candidates, k)
		}
	}
	for i := range ks.Keys {
		k := &ks.Keys[i]
		if k.Status == statusEnabled && k.Data != nil && k.OutputPrefixType == prefixRaw {
			candidates = append(candidates, k)
		}
	}
	for _, k := range candidates {
		body := ct[len(outputPrefix(k)):]
		key, err := keyValue(k.Data)
		if err != nil {
			continue
		}
		var out []byte
		switch {
		case deterministic && k.Data.TypeURL == typeURLAESSIV:
			out, err = sivDecrypt(key, body, aad)
		case !deterministic && k.Data.TypeURL == typeURLAESGCM:
			out, err = gcmDecrypt(key, body, aad)
		case !deterministic && k.Data.TypeURL == typeURLAESCBCPKCS:
			out, err = cbcDecrypt(key, body)
		default:
			continue
		}
		if err == nil {
			if out == nil {
				out = []byte{}
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("%s: decryption failed", name)
}

func gcmDecrypt(key, body, aad []byte) ([]byte, error) {
	if len(body) < gcmNonceSize {
		return nil, errors.New("ciphertext too short")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, body[:gcmNonceSize], body[gcmNonceSize:], aad)
}

func cbcDecrypt(key, body []byte) ([]byte, error) {
	if len(body) < 2*cbcIVSize || len(body)%aes.BlockSize != 0 {
		return nil, errors.New("invalid AES-CBC ciphertext")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(body)-cbcIVSize)
	cipher.NewCBCDecrypter(block, body[:cbcIVSize]).CryptBlocks(out, body[cbcIVSize:])
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, c := range out[len(out)-pad:] {
		if int(c) != pad {
			return nil, errors.New("invalid PKCS#7 padding")
		}
	}
	return out[:len(out)-pad], nil
}

// --- AES-SIV (RFC 5297) ---

func cmac(block cipher.Block, msg []byte) []byte {
	const bs = aes.BlockSize
	var l, k1, k2 [bs]byte
	block.Encrypt(l[:], l[:])
	dbl(k1[:], l[:])
	dbl(k2[:], k1[:])
	n := (len(msg) + bs - 1) / bs
	complete := n > 0 && len(msg)%bs == 0
	if n == 0 {
		n = 1
	}
	var last [bs]byte
	if complete {
		copy(last[:], msg[(n-1)*bs:])
		subtle.XORBytes(last[:], last[:], k1[:])
	} else {
		rem := msg[(n-1)*bs:]
		copy(last[:], rem)
		last[len(rem)] = 0x80
		subtle.XORBytes(last[:], last[:], k2[:])
	}
	var x [bs]byte
	for i := 0; i < n-1; i++ {
		subtle.XORBytes(x[:], x[:], msg[i*bs:(i+1)*bs])
		block.Encrypt(x[:], x[:])
	}
	subtle.XORBytes(x[:], x[:], last[:])
	block.Encrypt(x[:], x[:])
	return x[:]
}

// dbl is multiplication by x in GF(2^128).
func dbl(dst, src []byte) {
	carry := src[0] >> 7
	for i := 0; i < len(src)-1; i++ {
		dst[i] = src[i]<<1 | src[i+1]>>7
	}
	dst[len(src)-1] = src[len(src)-1] << 1
	if carry != 0 {
		dst[len(src)-1] ^= 0x87
	}
}

func s2v(block cipher.Block, aad, plaintext []byte) []byte {
	const bs = aes.BlockSize
	var zero [bs]byte
	d := cmac(block, zero[:])
	var tmp [bs]byte
	dbl(tmp[:], d)
	subtle.XORBytes(tmp[:], tmp[:], cmac(block, aad))
	d = tmp[:]
	if len(plaintext) >= bs {
		t := append([]byte(nil), plaintext...)
		subtle.XORBytes(t[len(t)-bs:], t[len(t)-bs:], d)
		return cmac(block, t)
	}
	var t [bs]byte
	dbl(t[:], d)
	var padded [bs]byte
	copy(padded[:], plaintext)
	padded[len(plaintext)] = 0x80
	subtle.XORBytes(t[:], t[:], padded[:])
	return cmac(block, t[:])
}

func sivCTR(key, iv, in []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	q := append([]byte(nil), iv...)
	q[8] &= 0x7f
	q[12] &= 0x7f
	out := make([]byte, len(in))
	cipher.NewCTR(block, q).XORKeyStream(out, in)
	return out, nil
}

func sivEncrypt(key, plaintext, aad []byte) ([]byte, error) {
	if len(key) != 32 && len(key) != 64 {
		return nil, fmt.Errorf("invalid AES-SIV key size %d", len(key))
	}
	macBlock, err := aes.NewCipher(key[:len(key)/2])
	if err != nil {
		return nil, err
	}
	iv := s2v(macBlock, aad, plaintext)
	ct, err := sivCTR(key[len(key)/2:], iv, plaintext)
	if err != nil {
		return nil, err
	}
	return append(iv, ct...), nil
}

func sivDecrypt(key, body, aad []byte) ([]byte, error) {
	if len(key) != 32 && len(key) != 64 {
		return nil, fmt.Errorf("invalid AES-SIV key size %d", len(key))
	}
	if len(body) < aes.BlockSize {
		return nil, errors.New("ciphertext too short")
	}
	iv := body[:aes.BlockSize]
	pt, err := sivCTR(key[len(key)/2:], iv, body[aes.BlockSize:])
	if err != nil {
		return nil, err
	}
	macBlock, err := aes.NewCipher(key[:len(key)/2])
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(s2v(macBlock, aad, pt), iv) != 1 {
		return nil, errors.New("AES-SIV authentication failed")
	}
	return pt, nil
}

// --- decrypt entry points ---

// BindAeadDecryptBytes implements AEAD.DECRYPT_BYTES.
func BindAeadDecryptBytes(args ...value.Value) (value.Value, error) {
	out, err := decryptCommon("AEAD.DECRYPT_BYTES", false, args)
	if err != nil || out == nil {
		return nil, err
	}
	return value.BytesValue(out), nil
}

// BindAeadDecryptString implements AEAD.DECRYPT_STRING.
func BindAeadDecryptString(args ...value.Value) (value.Value, error) {
	out, err := decryptCommon("AEAD.DECRYPT_STRING", false, args)
	if err != nil || out == nil {
		return nil, err
	}
	if !utf8.Valid(out) {
		return nil, fmt.Errorf("AEAD.DECRYPT_STRING failed: Decrypted plaintext is not a valid UTF-8 string. To decrypt to BYTES, use AEAD.DECRYPT_BYTES") //nolint:staticcheck // BigQuery's error text
	}
	return value.StringValue(string(out)), nil
}

// BindDeterministicDecryptBytes implements DETERMINISTIC_DECRYPT_BYTES.
func BindDeterministicDecryptBytes(args ...value.Value) (value.Value, error) {
	out, err := decryptCommon("DETERMINISTIC_DECRYPT_BYTES", true, args)
	if err != nil || out == nil {
		return nil, err
	}
	return value.BytesValue(out), nil
}

// BindDeterministicDecryptString implements DETERMINISTIC_DECRYPT_STRING.
func BindDeterministicDecryptString(args ...value.Value) (value.Value, error) {
	out, err := decryptCommon("DETERMINISTIC_DECRYPT_STRING", true, args)
	if err != nil || out == nil {
		return nil, err
	}
	if !utf8.Valid(out) {
		return nil, fmt.Errorf("DETERMINISTIC_DECRYPT_STRING failed: Decrypted plaintext is not a valid UTF-8 string. To decrypt to BYTES, use DETERMINISTIC_DECRYPT_BYTES") //nolint:staticcheck // BigQuery's error text
	}
	return value.StringValue(string(out)), nil
}
