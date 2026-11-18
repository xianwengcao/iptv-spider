package utils

import (
	"bytes"
)

// _padding 按 blockSize 做 PKCS#7 填充：补 padLen 个值为 padLen 的字节。
// AES 的 blockSize 恒为 16，因此这里不再保留"零填充"分支。
func _padding(cipherText []byte, blockSize int) []byte {
	length := (len(cipherText) + blockSize) / blockSize
	padLen := blockSize*length - len(cipherText)
	padText := bytes.Repeat([]byte{byte(padLen)}, padLen)
	return append(cipherText, padText...)
}

func PKCS7Padding(cipherText []byte) []byte {
	return _padding(cipherText, 16)
}
