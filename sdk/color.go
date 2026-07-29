package sdk

// Color is a truecolor pixel value, 0xRRGGBB.
type Color uint32

func (c Color) RGB() (r, g, b uint8) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c)
}

const (
	Black   Color = 0x000000
	White   Color = 0xf2f2f2
	Gray    Color = 0x8a8a8a
	DimGray Color = 0x3a3a3a
	Red     Color = 0xe64545
	Orange  Color = 0xf08c28
	Yellow  Color = 0xe6c945
	Green   Color = 0x4fc964
	Cyan    Color = 0x3fc4c9
	Blue    Color = 0x4f7de0
	Purple  Color = 0xa06ae0
	Pink    Color = 0xe066a8
)
