package main

import (
	"fmt"
	"testing"
)

func assert(exp bool, t *testing.T) {
	if !exp {
		t.Error("fail")
	}
}

func Must[T any](x T, err error) T {
	if err != nil {
		panic(err)
	}
	return x
}

func Test_parseSize(t *testing.T) {
	assert(Must(parseSize("12G")) == 12884901888, t)
	assert(Must(parseSize("12.12M")) == 12708741, t)
	assert(Must(parseSize("123456")) == 123456, t)
	assert(Must(parseSize("4k")) == 4096, t)
}

func Test_splitLines(t *testing.T) {
	fmt.Println(splitLines("hello world foo bar", 11))
}
