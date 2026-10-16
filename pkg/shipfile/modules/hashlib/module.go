// Package hashlibmod implements the Python "hashlib" module for gpython.
package hashlibmod

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"
	"strings"

	"github.com/go-python/gpython/py"
)

var hashType = py.NewType("HASH", "Secure hash and message digest object")

type hashObject struct {
	name       string
	digestSize int
	h          hash.Hash
	buf        []byte
}

func (h *hashObject) Type() *py.Type { return hashType }

func (h *hashObject) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "name":
		return py.String(h.name), nil
	case "digest_size":
		return py.Int(h.digestSize), nil
	case "update":
		return py.MustNewMethod("update", func(_ py.Object, args py.Tuple) (py.Object, error) {
			if len(args) != 1 {
				return nil, py.ExceptionNewf(py.TypeError, "update() takes exactly 1 argument (%d given)", len(args))
			}
			bytesData, err := extractBytes(args[0])
			if err != nil {
				return nil, err
			}
			h.buf = append(h.buf, bytesData...)
			h.h.Write(bytesData)
			return py.None, nil
		}, 0, "update(arg)\nUpdate the hash object with the bytes-like object."), nil
	case "hexdigest":
		return py.MustNewMethod("hexdigest", func(_ py.Object, args py.Tuple) (py.Object, error) {
			res := h.h.Sum(nil)
			return py.String(hex.EncodeToString(res)), nil
		}, 0, "hexdigest() -> str\nReturn the digest of the data passed to the update() method so far, as a string of hexadecimal digits."), nil
	case "digest":
		return py.MustNewMethod("digest", func(_ py.Object, args py.Tuple) (py.Object, error) {
			res := h.h.Sum(nil)
			return py.Bytes(res), nil
		}, 0, "digest() -> bytes\nReturn the digest of the data passed to the update() method so far, as a bytes object."), nil
	case "copy":
		return py.MustNewMethod("copy", func(_ py.Object, args py.Tuple) (py.Object, error) {
			copied := newHashInstance(h.name)
			if copied == nil {
				return nil, py.ExceptionNewf(py.ValueError, "unknown hash algorithm: %s", h.name)
			}
			if len(h.buf) > 0 {
				copied.buf = append([]byte(nil), h.buf...)
				copied.h.Write(copied.buf)
			}
			return copied, nil
		}, 0, "copy() -> hash object\nReturn a copy of the hash object."), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'%s' object has no attribute '%s'", h.Type().Name, name)
}

func extractBytes(obj py.Object) ([]byte, error) {
	switch v := obj.(type) {
	case py.Bytes:
		return []byte(v), nil
	case py.String:
		return []byte(v), nil
	default:
		return nil, py.ExceptionNewf(py.TypeError, "object supporting the buffer API required, not '%s'", obj.Type().Name)
	}
}

func newHashInstance(name string) *hashObject {
	switch strings.ToLower(name) {
	case "sha256":
		return &hashObject{name: "sha256", digestSize: 32, h: sha256.New()}
	case "sha512":
		return &hashObject{name: "sha512", digestSize: 64, h: sha512.New()}
	case "sha1":
		return &hashObject{name: "sha1", digestSize: 20, h: sha1.New()}
	case "md5":
		return &hashObject{name: "md5", digestSize: 16, h: md5.New()}
	default:
		return nil
	}
}

func makeHashConstructor(algo string) func(py.Object, py.Tuple, py.StringDict) (py.Object, error) {
	return func(_ py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
		ho := newHashInstance(algo)
		if ho == nil {
			return nil, py.ExceptionNewf(py.ValueError, "unsupported hash algorithm: %s", algo)
		}
		var dataObj py.Object
		if len(args) > 0 {
			dataObj = args[0]
		} else if d, ok := kwargs["data"]; ok {
			dataObj = d
		}
		if dataObj != nil {
			b, err := extractBytes(dataObj)
			if err != nil {
				return nil, err
			}
			ho.buf = append(ho.buf, b...)
			ho.h.Write(b)
		}
		return ho, nil
	}
}

func hashlibNew(_ py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "new() requires at least 1 argument (name)")
	}
	nameStr, ok := args[0].(py.String)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "name must be a string")
	}
	ho := newHashInstance(string(nameStr))
	if ho == nil {
		return nil, py.ExceptionNewf(py.ValueError, "unsupported hash algorithm: %s", string(nameStr))
	}
	var dataObj py.Object
	if len(args) > 1 {
		dataObj = args[1]
	} else if d, ok := kwargs["data"]; ok {
		dataObj = d
	}
	if dataObj != nil {
		b, err := extractBytes(dataObj)
		if err != nil {
			return nil, err
		}
		ho.buf = append(ho.buf, b...)
		ho.h.Write(b)
	}
	return ho, nil
}

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "hashlib",
			Doc:  "Generic hashing library (sha256, sha512, sha1, md5).",
		},
		Methods: []*py.Method{
			py.MustNewMethod("sha256", makeHashConstructor("sha256"), 0, "sha256([data]) -> hash object"),
			py.MustNewMethod("sha512", makeHashConstructor("sha512"), 0, "sha512([data]) -> hash object"),
			py.MustNewMethod("sha1", makeHashConstructor("sha1"), 0, "sha1([data]) -> hash object"),
			py.MustNewMethod("md5", makeHashConstructor("md5"), 0, "md5([data]) -> hash object"),
			py.MustNewMethod("new", hashlibNew, 0, "new(name, [data]) -> hash object"),
		},
		Globals: py.StringDict{
			"algorithms_available": &py.List{Items: []py.Object{py.String("md5"), py.String("sha1"), py.String("sha256"), py.String("sha512")}},
			"algorithms_guaranteed": &py.List{Items: []py.Object{py.String("md5"), py.String("sha1"), py.String("sha256"), py.String("sha512")}},
		},
	})
}
