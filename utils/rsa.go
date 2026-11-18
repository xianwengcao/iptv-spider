package utils

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
)

const priKey = `-----BEGIN PRIVATE KEY-----
MIICdwIBADANBgkqhkiG9w0BAQEFAASCAmEwggJdAgEAAoGBAKwXs+z2tv9LDonG
jyDFWLXB9IzubHJglq/WRWMnFSb5koXu6uLpuJ6ax0MN2HG7JmDO5mT8pZn+6ayL
5HklZ43upnOCbsg3K/tFuEv4CCscztJVt/rC2y9NPJhWYfy/GxdywcAbds6mqjTY
QWbvtJv00qac1QOPprgYez4KxU6dAgMBAAECgYAmTnBigthhI1ftGyGo7cS9UJsa
88d3/kAMi+mOFJkEv/D5lyD5uYS66UEJj/9p8XqteeCXAhXqnW9uVQVaYhUWiS9l
Ois73zUpQNBT48kAMAhn7ux6gpgtSaKwSHsbKUs8YoMe0FoPKIPwln7XLhvLCKpm
5knjWD3gtVQEW8mxAQJBAOAyMMmW17fMLeSdpqskvElh63heiyb+qsmkldjYxRhK
gMK4hlI/6CFX5eIWrZUXJ1cf6UOaJFgBXQ/qxRmkSI0CQQDEgVg+jmjopestab4l
tEM3gzls4+eocwRgfzKEouo4SUtEk6BZhPcuk6Pss10Z+9jqiUMgo6TZJHYkfTWg
7kJRAkAPIlQ4x334YkgWzq2Zj/lF2t5SWc966mYNBpc29CsZ4K2gd2RZ2QaKeayC
/pTpI478SqMsdRNO/YiSsn5rpLNhAkEAttCK82/0A/VQjWhiIZvKKRwpUbfZ7qpK
uSe9LQ6QDxuJLdyWApKkkC2FBRJ9nE3kqZZX4Ea+d9HnI91lBjqDcQJBAN9v1q9e
TIM7EPiRNDlEbvJQ39+6PT8zX8EE9CHAz4mBGp4W7szMas2561xi5iN/ZC9ssUiM
jBrqDb4pgDZAFOc=
-----END PRIVATE KEY-----`

func GetRSAPriKey() []byte {
	pub, _ := pem.Decode([]byte(priKey))
	return pub.Bytes
}

type RSA struct {
	priKey *rsa.PrivateKey
}

// LoadPriKey 加载私钥
func (r *RSA) LoadPriKey(key []byte) {
	priKey, err := x509.ParsePKCS8PrivateKey(key)
	if err != nil {
		priKey, _ = x509.ParsePKCS1PrivateKey(key)
	}
	r.priKey = priKey.(*rsa.PrivateKey)
}

// PriEncrypt 私钥加密（RSA sign，签名结果即密文）
func (r *RSA) PriEncrypt(data []byte) []byte {
	result, err := rsa.SignPKCS1v15(rand.Reader, r.priKey, crypto.Hash(0), data)
	if err != nil {
		return nil
	}
	return result
}
