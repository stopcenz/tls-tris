// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tls

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
)

// sessionState contains the information that is serialized into a session
// ticket in order to later resume a connection.
type sessionState struct {
	vers         uint16
	cipherSuite  uint16
	masterSecret []byte
	certificates [][]byte
	// usedOldKey is true if the ticket from which this session came from
	// was encrypted with an older key and thus should be refreshed.
	usedOldKey bool
}

func (s *sessionState) equal(i interface{}) bool {
	s1, ok := i.(*sessionState)
	if !ok {
		return false
	}

	if s.vers != s1.vers ||
		s.cipherSuite != s1.cipherSuite ||
		!bytes.Equal(s.masterSecret, s1.masterSecret) {
		return false
	}

	if len(s.certificates) != len(s1.certificates) {
		return false
	}

	for i := range s.certificates {
		if !bytes.Equal(s.certificates[i], s1.certificates[i]) {
			return false
		}
	}

	return true
}

func (s *sessionState) marshal() []byte {
	length := 2 + 2 + 2 + len(s.masterSecret) + 2
	for _, cert := range s.certificates {
		length += 4 + len(cert)
	}

	ret := make([]byte, length)
	x := ret
	x[0] = byte(s.vers >> 8)
	x[1] = byte(s.vers)
	x[2] = byte(s.cipherSuite >> 8)
	x[3] = byte(s.cipherSuite)
	x[4] = byte(len(s.masterSecret) >> 8)
	x[5] = byte(len(s.masterSecret))
	x = x[6:]
	copy(x, s.masterSecret)
	x = x[len(s.masterSecret):]

	x[0] = byte(len(s.certificates) >> 8)
	x[1] = byte(len(s.certificates))
	x = x[2:]

	for _, cert := range s.certificates {
		x[0] = byte(len(cert) >> 24)
		x[1] = byte(len(cert) >> 16)
		x[2] = byte(len(cert) >> 8)
		x[3] = byte(len(cert))
		copy(x[4:], cert)
		x = x[4+len(cert):]
	}

	return ret
}

func (s *sessionState) unmarshal(data []byte) bool {
	if len(data) < 8 {
		return false
	}

	s.vers = uint16(data[0])<<8 | uint16(data[1])
	s.cipherSuite = uint16(data[2])<<8 | uint16(data[3])
	masterSecretLen := int(data[4])<<8 | int(data[5])
	data = data[6:]
	if len(data) < masterSecretLen {
		return false
	}

	s.masterSecret = data[:masterSecretLen]
	data = data[masterSecretLen:]

	if len(data) < 2 {
		return false
	}

	numCerts := int(data[0])<<8 | int(data[1])
	data = data[2:]

	s.certificates = make([][]byte, numCerts)
	for i := range s.certificates {
		if len(data) < 4 {
			return false
		}
		certLen := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
		data = data[4:]
		if certLen < 0 {
			return false
		}
		if len(data) < certLen {
			return false
		}
		s.certificates[i] = data[:certLen]
		data = data[certLen:]
	}

	return len(data) == 0
}

type sessionState13 struct {
	vers             uint16
	suite            uint16
	ageAdd           uint32
	createdAt        uint64
	maxEarlyDataLen  uint32
	resumptionSecret []byte
	alpnProtocol     string
	SNI              string
}

func (s *sessionState13) equal(i interface{}) bool {
	s1, ok := i.(*sessionState13)
	if !ok {
		return false
	}

	return s.vers == s1.vers &&
		s.suite == s1.suite &&
		s.ageAdd == s1.ageAdd &&
		s.createdAt == s1.createdAt &&
		s.maxEarlyDataLen == s1.maxEarlyDataLen &&
		bytes.Equal(s.resumptionSecret, s1.resumptionSecret) &&
		s.alpnProtocol == s1.alpnProtocol &&
		s.SNI == s1.SNI
}

func (s *sessionState13) marshal() []byte {
	length := 2 + 2 + 4 + 8 + 4 + 2 + len(s.resumptionSecret) + 2 + len(s.alpnProtocol) + 2 + len(s.SNI)

	x := make([]byte, length)
	x[0] = byte(s.vers >> 8)
	x[1] = byte(s.vers)
	x[2] = byte(s.suite >> 8)
	x[3] = byte(s.suite)
	x[4] = byte(s.ageAdd >> 24)
	x[5] = byte(s.ageAdd >> 16)
	x[6] = byte(s.ageAdd >> 8)
	x[7] = byte(s.ageAdd)
	x[8] = byte(s.createdAt >> 56)
	x[9] = byte(s.createdAt >> 48)
	x[10] = byte(s.createdAt >> 40)
	x[11] = byte(s.createdAt >> 32)
	x[12] = byte(s.createdAt >> 24)
	x[13] = byte(s.createdAt >> 16)
	x[14] = byte(s.createdAt >> 8)
	x[15] = byte(s.createdAt)
	x[16] = byte(s.maxEarlyDataLen >> 24)
	x[17] = byte(s.maxEarlyDataLen >> 16)
	x[18] = byte(s.maxEarlyDataLen >> 8)
	x[19] = byte(s.maxEarlyDataLen)
	x[20] = byte(len(s.resumptionSecret) >> 8)
	x[21] = byte(len(s.resumptionSecret))
	copy(x[22:], s.resumptionSecret)
	z := x[22+len(s.resumptionSecret):]
	z[0] = byte(len(s.alpnProtocol) >> 8)
	z[1] = byte(len(s.alpnProtocol))
	copy(z[2:], s.alpnProtocol)
	z = z[2+len(s.alpnProtocol):]
	z[0] = byte(len(s.SNI) >> 8)
	z[1] = byte(len(s.SNI))
	copy(z[2:], s.SNI)

	return x
}

func (s *sessionState13) unmarshal(data []byte) bool {
	if len(data) < 24 {
		return false
	}

	s.vers = uint16(data[0])<<8 | uint16(data[1])
	s.suite = uint16(data[2])<<8 | uint16(data[3])
	s.ageAdd = uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])
	s.createdAt = uint64(data[8])<<56 | uint64(data[9])<<48 | uint64(data[10])<<40 | uint64(data[11])<<32 |
		uint64(data[12])<<24 | uint64(data[13])<<16 | uint64(data[14])<<8 | uint64(data[15])
	s.maxEarlyDataLen = uint32(data[16])<<24 | uint32(data[17])<<16 | uint32(data[18])<<8 | uint32(data[19])

	l := int(data[20])<<8 | int(data[21])
	if len(data) < 22+l+2 {
		return false
	}
	s.resumptionSecret = data[22 : 22+l]
	z := data[22+l:]

	l = int(z[0])<<8 | int(z[1])
	if len(z) < 2+l+2 {
		return false
	}
	s.alpnProtocol = string(z[2 : 2+l])
	z = z[2+l:]

	l = int(z[0])<<8 | int(z[1])
	if len(z) != 2+l {
		return false
	}
	s.SNI = string(z[2 : 2+l])

	return true
}

func (c *Conn) encryptTicket(serialized []byte) ([]byte, error) {
	encrypted := make([]byte, ticketKeyNameLen+aes.BlockSize+len(serialized)+sha256.Size)
	keyName := encrypted[:ticketKeyNameLen]
	iv := encrypted[ticketKeyNameLen : ticketKeyNameLen+aes.BlockSize]
	macBytes := encrypted[len(encrypted)-sha256.Size:]

	if _, err := io.ReadFull(c.config.rand(), iv); err != nil {
		return nil, err
	}
	key := c.config.ticketKeys()[0]
	copy(keyName, key.keyName[:])
	block, err := aes.NewCipher(key.aesKey[:])
	if err != nil {
		return nil, errors.New("tls: failed to create cipher while encrypting ticket: " + err.Error())
	}
	cipher.NewCTR(block, iv).XORKeyStream(encrypted[ticketKeyNameLen+aes.BlockSize:], serialized)

	mac := hmac.New(sha256.New, key.hmacKey[:])
	mac.Write(encrypted[:len(encrypted)-sha256.Size])
	mac.Sum(macBytes[:0])

	return encrypted, nil
}

func (c *Conn) decryptTicket(encrypted []byte) (serialized []byte, usedOldKey bool) {
	if c.config.SessionTicketsDisabled ||
		len(encrypted) < ticketKeyNameLen+aes.BlockSize+sha256.Size {
		return nil, false
	}

	keyName := encrypted[:ticketKeyNameLen]
	iv := encrypted[ticketKeyNameLen : ticketKeyNameLen+aes.BlockSize]
	macBytes := encrypted[len(encrypted)-sha256.Size:]

	keys := c.config.ticketKeys()
	keyIndex := -1
	for i, candidateKey := range keys {
		if bytes.Equal(keyName, candidateKey.keyName[:]) {
			keyIndex = i
			break
		}
	}

	if keyIndex == -1 {
		return nil, false
	}
	key := &keys[keyIndex]

	mac := hmac.New(sha256.New, key.hmacKey[:])
	mac.Write(encrypted[:len(encrypted)-sha256.Size])
	expected := mac.Sum(nil)

	if subtle.ConstantTimeCompare(macBytes, expected) != 1 {
		return nil, false
	}

	block, err := aes.NewCipher(key.aesKey[:])
	if err != nil {
		return nil, false
	}
	ciphertext := encrypted[ticketKeyNameLen+aes.BlockSize : len(encrypted)-sha256.Size]
	plaintext := ciphertext
	cipher.NewCTR(block, iv).XORKeyStream(plaintext, ciphertext)

	return plaintext, keyIndex > 0
}
