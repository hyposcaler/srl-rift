package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Security envelope constants per RFC 9692 Section 6.9.3.
const (
	RIFTMagic    uint16 = 0xA1F7
	EnvelopeMinLen      = 2 + 2 + 1 + 1 + 2 + 2 + 4 // magic + pktnum + ver + outerkey + nonce_local + nonce_remote + remaining_lifetime (min without fingerprint)
)

// SecurityEnvelope wraps a serialized ProtocolPacket.
// For M0, we use no authentication (outer key = undefined_securitykey_id).
type SecurityEnvelope struct {
	PacketNumber      PacketNumberType
	OuterKeyID        OuterSecurityKeyID
	NonceLocal        NonceType
	NonceRemote       NonceType
	RemainingLifetime LifeTimeInSecType
	// TIE origin security envelope header (optional, only for TIE packets)
	TIEOrigin *TIEOriginSecurityEnvelopeHeader
	// The serialized ProtocolPacket payload
	Payload []byte
}

// TIEOriginSecurityEnvelopeHeader is present when the packet is a TIE.
type TIEOriginSecurityEnvelopeHeader struct {
	TIEOriginKeyID    TIESecurityKeyID
	FingerprintLength uint16
	Fingerprint       []byte
}

// EncodeEnvelope writes a security envelope with no authentication.
func EncodeEnvelope(w io.Writer, env *SecurityEnvelope) error {
	var buf [4]byte

	// Magic
	binary.BigEndian.PutUint16(buf[:2], RIFTMagic)
	if _, err := w.Write(buf[:2]); err != nil {
		return err
	}
	// Packet number
	binary.BigEndian.PutUint16(buf[:2], uint16(env.PacketNumber))
	if _, err := w.Write(buf[:2]); err != nil {
		return err
	}
	// Protocol major version
	buf[0] = byte(ProtocolMajorVersion)
	if _, err := w.Write(buf[:1]); err != nil {
		return err
	}
	// Outer key ID
	buf[0] = byte(env.OuterKeyID)
	if _, err := w.Write(buf[:1]); err != nil {
		return err
	}
	// Fingerprint length = 0 (no auth)
	binary.BigEndian.PutUint16(buf[:2], 0)
	if _, err := w.Write(buf[:2]); err != nil {
		return err
	}
	// No fingerprint bytes (length = 0)

	// Nonce local
	binary.BigEndian.PutUint16(buf[:2], uint16(env.NonceLocal))
	if _, err := w.Write(buf[:2]); err != nil {
		return err
	}
	// Nonce remote
	binary.BigEndian.PutUint16(buf[:2], uint16(env.NonceRemote))
	if _, err := w.Write(buf[:2]); err != nil {
		return err
	}
	// Remaining lifetime
	binary.BigEndian.PutUint32(buf[:4], uint32(env.RemainingLifetime))
	if _, err := w.Write(buf[:4]); err != nil {
		return err
	}

	// TIE origin header (if present)
	if env.TIEOrigin != nil {
		binary.BigEndian.PutUint32(buf[:4], uint32(env.TIEOrigin.TIEOriginKeyID))
		if _, err := w.Write(buf[:4]); err != nil {
			return err
		}
		binary.BigEndian.PutUint16(buf[:2], env.TIEOrigin.FingerprintLength)
		if _, err := w.Write(buf[:2]); err != nil {
			return err
		}
		if env.TIEOrigin.FingerprintLength > 0 {
			if _, err := w.Write(env.TIEOrigin.Fingerprint); err != nil {
				return err
			}
		}
	}

	// Payload (serialized ProtocolPacket)
	_, err := w.Write(env.Payload)
	return err
}

// DecodeEnvelope reads a security envelope from r.
func DecodeEnvelope(r io.Reader) (*SecurityEnvelope, error) {
	var buf [4]byte
	env := &SecurityEnvelope{}

	// Magic
	if _, err := io.ReadFull(r, buf[:2]); err != nil {
		return nil, err
	}
	magic := binary.BigEndian.Uint16(buf[:2])
	if magic != RIFTMagic {
		return nil, fmt.Errorf("bad RIFT magic: 0x%04X", magic)
	}

	// Packet number
	if _, err := io.ReadFull(r, buf[:2]); err != nil {
		return nil, err
	}
	env.PacketNumber = PacketNumberType(binary.BigEndian.Uint16(buf[:2]))

	// Major version
	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return nil, err
	}
	// We just consume version, don't validate in M0.

	// Outer key ID
	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return nil, err
	}
	env.OuterKeyID = OuterSecurityKeyID(buf[0])

	// Fingerprint length
	if _, err := io.ReadFull(r, buf[:2]); err != nil {
		return nil, err
	}
	fpLen := binary.BigEndian.Uint16(buf[:2])

	// Skip fingerprint
	if fpLen > 0 {
		fp := make([]byte, fpLen)
		if _, err := io.ReadFull(r, fp); err != nil {
			return nil, err
		}
	}

	// Nonce local
	if _, err := io.ReadFull(r, buf[:2]); err != nil {
		return nil, err
	}
	env.NonceLocal = NonceType(binary.BigEndian.Uint16(buf[:2]))

	// Nonce remote
	if _, err := io.ReadFull(r, buf[:2]); err != nil {
		return nil, err
	}
	env.NonceRemote = NonceType(binary.BigEndian.Uint16(buf[:2]))

	// Remaining lifetime
	if _, err := io.ReadFull(r, buf[:4]); err != nil {
		return nil, err
	}
	env.RemainingLifetime = LifeTimeInSecType(binary.BigEndian.Uint32(buf[:4]))

	// The rest is the serialized ProtocolPacket payload.
	// We read all remaining bytes.
	payload, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	env.Payload = payload

	return env, nil
}
