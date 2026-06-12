// Package secretbox 提供 model_configs per-config API key 的静态加密 (BYOK 基础设施)。
// 使用 stdlib AES-256-GCM：主密钥来自环境变量 STUDIO_CONFIG_ENC_KEY (base64 编码的
// 32 字节)。未设置时返回 disabled Box——加密被拒绝 (ErrNoKey)，调用方据此禁止存 key。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// EnvMasterKey 是 base64 编码的 32 字节 AES-256 主密钥环境变量名。
const EnvMasterKey = "STUDIO_CONFIG_ENC_KEY"

// ErrNoKey 表示 Box 未配置主密钥 (disabled)，无法加密。
var ErrNoKey = errors.New("secretbox: no master key configured (set STUDIO_CONFIG_ENC_KEY)")

// Box 用主密钥做 AES-256-GCM 加解密。零值/disabled box 的 Encrypt 返回 ErrNoKey。
type Box struct {
	aead cipher.AEAD // nil = disabled
}

// New 用 base64 编码的 32 字节主密钥构造 Box。masterKeyB64 为空时返回 disabled box
// (非错误)；非空但解码后不是 32 字节时返回错误。
func New(masterKeyB64 string) (*Box, error) {
	if masterKeyB64 == "" {
		return &Box{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return nil, fmt.Errorf("secretbox: decode master key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secretbox: master key must be 32 bytes (AES-256), got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// NewBoxFromEnv 从 STUDIO_CONFIG_ENC_KEY 读取主密钥构造 Box。未设置时返回 disabled box。
func NewBoxFromEnv() (*Box, error) {
	return New(os.Getenv(EnvMasterKey))
}

// Enabled 报告 Box 是否配置了主密钥。
func (b *Box) Enabled() bool { return b != nil && b.aead != nil }

// Encrypt 用随机 nonce 做 AES-256-GCM 加密，输出 nonce||ciphertext (自带 nonce)。
// disabled box 返回 ErrNoKey。
func (b *Box) Encrypt(plaintext []byte) ([]byte, error) {
	if !b.Enabled() {
		return nil, ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	// Seal 把密文追加到 nonce 之后，得到 nonce||ciphertext。
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 反解 nonce||ciphertext。disabled box 返回 ErrNoKey；篡改 (GCM 校验失败) 返回错误。
func (b *Box) Decrypt(ciphertext []byte) ([]byte, error) {
	if !b.Enabled() {
		return nil, ErrNoKey
	}
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	pt, err := b.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("secretbox: open: %w", err)
	}
	return pt, nil
}
