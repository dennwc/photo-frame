package main

import (
	"errors"
	"fmt"
	"image"
	"strings"

	"github.com/go-gl/gl/all-core/gl"
)

func calcScale(dst, src image.Point) (sw, sh float64) {
	if dst.X == 0 || dst.Y == 0 {
		return 1, 1
	} else if src.X == 0 || src.Y == 0 {
		return 1, 1
	}
	dstAspect := float64(dst.X) / float64(dst.Y)
	srcAspect := float64(src.X) / float64(src.Y)
	sw, sh = 1, 1
	if dstAspect <= srcAspect {
		sh = dstAspect / srcAspect
	} else {
		sw = srcAspect / dstAspect
	}
	return sw, sh
}

func calcOffset(sw, sh float64, w, h float64) (ox, oy float64) {
	ox = (1 - sw) * w / 2
	oy = (1 - sh) * h / 2
	return
}

func glCheckErr() {
	if e := gl.GetError(); e != 0 {
		panic(fmt.Errorf("gl error: %x", e))
	}
}

func compileProgram(vert, frag uint32) (uint32, error) {
	prog := gl.CreateProgram()
	gl.AttachShader(prog, vert)
	gl.AttachShader(prog, frag)
	gl.BindFragDataLocation(prog, 0, gl.Str("color\x00"))
	gl.LinkProgram(prog)

	var st int32
	gl.GetProgramiv(prog, gl.LINK_STATUS, &st)
	if st == gl.FALSE {
		var sz int32
		gl.GetProgramiv(prog, gl.INFO_LOG_LENGTH, &sz)
		text := make([]byte, sz+1)
		gl.GetProgramInfoLog(prog, sz, nil, &text[0])
		return 0, errors.New(string(text))
	}
	return prog, nil
}

func compileShader(typ uint32, src string) (uint32, error) {
	if !strings.HasSuffix(src, "\x00") {
		src += "\x00"
	}
	s := gl.CreateShader(typ)
	cstr, free := gl.Strs(src)
	gl.ShaderSource(s, 1, cstr, nil)
	free()
	gl.CompileShader(s)
	var st int32
	gl.GetShaderiv(s, gl.COMPILE_STATUS, &st)
	if st == gl.FALSE {
		var sz int32
		gl.GetShaderiv(s, gl.INFO_LOG_LENGTH, &sz)
		text := make([]byte, sz+1)
		gl.GetShaderInfoLog(s, sz, nil, &text[0])
		return 0, errors.New(string(text))
	}
	return s, nil
}

func glSetProgAttr(attr uint32, sz int, typ uint32, norm bool, stride, offset uintptr) {
	gl.EnableVertexAttribArray(attr)
	glCheckErr()
	gl.VertexAttribPointerWithOffset(attr, int32(sz), typ, norm, int32(stride), offset)
	glCheckErr()
}
