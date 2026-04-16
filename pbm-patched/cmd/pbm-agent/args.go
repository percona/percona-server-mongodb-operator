package main

import (
	"log"
	"net/url"
	"os"
	"reflect"
	"strings"
	"unsafe"
)

const mauthPrefix = "--" + mongoConnFlag + "="

func setarg(i int, as string) {
	ptr := (*reflect.StringHeader)(unsafe.Pointer(&os.Args[i]))
	arg := (*[1 << 30]byte)(unsafe.Pointer(ptr.Data))[:ptr.Len]

	n := copy(arg, as)
	for ; n < len(arg); n++ {
		arg[n] = 0
	}
}

// hidecreds replaces creds (user & pass) with bunch of `x` if there are any in mongo connection flags
func hidecreds() {
	for k, v := range os.Args {
		if strings.HasPrefix(v, mauthPrefix) {
			str := strings.TrimPrefix(v, mauthPrefix)
			u, err := url.Parse(str)
			if err != nil {
				log.Println(err)
				return
			}
			if u.User == nil || u.User.String() == "" {
				return
			}

			var xuser, xpass []byte
			xuser = make([]byte, len(u.User.Username()))
			p, _ := u.User.Password()
			xpass = make([]byte, len(p))

			padx(xuser)
			padx(xpass)

			u.User = url.UserPassword(string(xuser), string(xpass))

			setarg(k, mauthPrefix+u.String())
		}
	}
}

func padx(s []byte) {
	for i := 0; i < len(s); i++ {
		s[i] = 'x'
	}
}
