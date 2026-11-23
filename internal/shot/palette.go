package shot

// rgb is an opaque colour.
type rgb struct{ r, g, b uint8 }

func hex(v uint32) rgb { return rgb{uint8(v >> 16), uint8(v >> 8), uint8(v)} }

// mix returns a*(1-t) + b*t, with t in 0..256.
func mix(a, b rgb, t int) rgb {
	m := func(x, y uint8) uint8 { return uint8((int(x)*(256-t) + int(y)*t) >> 8) }
	return rgb{m(a.r, b.r), m(a.g, b.g), m(a.b, b.b)}
}

// palette is the look of one terminal theme.
type palette struct {
	fg, bg  rgb     // default colours
	ansi    [16]rgb // the 16 colours of SGR 30-37, 90-97 and 256-colour 0-15
	bar     rgb     // title bar
	barLine rgb     // line under the title bar
	title   rgb     // title text
}

// The palettes follow the GitHub themes, which the TUI colours also use.
var (
	darkPalette = palette{
		fg: hex(0xe6edf3), bg: hex(0x0d1117),
		ansi: [16]rgb{
			hex(0x484f58), hex(0xff7b72), hex(0x3fb950), hex(0xd29922),
			hex(0x58a6ff), hex(0xbc8cff), hex(0x39c5cf), hex(0xb1bac4),
			hex(0x6e7681), hex(0xffa198), hex(0x56d364), hex(0xe3b341),
			hex(0x79c0ff), hex(0xd2a8ff), hex(0x56d4dd), hex(0xf0f6fc),
		},
		bar: hex(0x1c2128), barLine: hex(0x30363d), title: hex(0x9198a1),
	}
	lightPalette = palette{
		fg: hex(0x1f2328), bg: hex(0xffffff),
		ansi: [16]rgb{
			hex(0x24292f), hex(0xcf222e), hex(0x116329), hex(0x9a6700),
			hex(0x0969da), hex(0x8250df), hex(0x1b7c83), hex(0x6e7781),
			hex(0x57606a), hex(0xa40e26), hex(0x1a7f37), hex(0xd4a72c),
			hex(0x218bff), hex(0xa475f9), hex(0x3192aa), hex(0x8c959f),
		},
		bar: hex(0xeaeef2), barLine: hex(0xd0d7de), title: hex(0x59636e),
	}
)

// The three window dots: close, minimise, zoom.
var dotColors = [3]rgb{hex(0xff5f57), hex(0xfebc2e), hex(0x28c840)}

// xterm256 returns colour n (16-255) of the xterm 256-colour table.
func xterm256(n int) rgb {
	if n >= 232 {
		v := uint8(8 + 10*(n-232))
		return rgb{v, v, v}
	}
	n -= 16
	step := func(i int) uint8 {
		if i == 0 {
			return 0
		}
		return uint8(55 + 40*i)
	}
	return rgb{step(n / 36), step(n / 6 % 6), step(n % 6)}
}

// colour is a cell colour before the palette resolves it.
type colour struct {
	kind uint8 // colDefault, colIndex or colRGB
	idx  uint8
	rgb  rgb
}

const (
	colDefault uint8 = iota
	colIndex
	colRGB
)

// resolve returns the RGB value of c. def is the default colour. bold
// makes the 8 base colours bright, as most terminals do.
func (p *palette) resolve(c colour, def rgb, bold bool) rgb {
	switch c.kind {
	case colIndex:
		n := int(c.idx)
		if n < 16 {
			if bold && n < 8 {
				n += 8
			}
			return p.ansi[n]
		}
		return xterm256(n)
	case colRGB:
		return c.rgb
	}
	return def
}
