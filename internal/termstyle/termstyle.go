package termstyle

import (
	"fmt"
	"io"
	"os"

	"scenery.sh/internal/envpolicy"
)

const (
	resetCode   = "\x1b[0m"
	boldCode    = "\x1b[1m"
	dimCode     = "\x1b[2m"
	redCode     = "\x1b[31m"
	greenCode   = "\x1b[32m"
	yellowCode  = "\x1b[33m"
	blueCode    = "\x1b[34m"
	magentaCode = "\x1b[35m"
	cyanCode    = "\x1b[36m"
	inverseCode = "\x1b[7m"
)

type Palette struct {
	enabled bool
}

func New(w io.Writer) Palette {
	return Palette{enabled: useColor(w)}
}

func (p Palette) Enabled() bool {
	return p.enabled
}

func (p Palette) Bold(text string) string {
	return p.wrap(boldCode, text)
}

func (p Palette) Dim(text string) string {
	return p.wrap(dimCode, text)
}

func (p Palette) Red(text string) string {
	return p.wrap(redCode, text)
}

func (p Palette) Green(text string) string {
	return p.wrap(greenCode, text)
}

func (p Palette) Yellow(text string) string {
	return p.wrap(yellowCode, text)
}

func (p Palette) Blue(text string) string {
	return p.wrap(blueCode, text)
}

func (p Palette) Magenta(text string) string {
	return p.wrap(magentaCode, text)
}

func (p Palette) Cyan(text string) string {
	return p.wrap(cyanCode, text)
}

func (p Palette) Inverse(text string) string {
	return p.wrap(inverseCode, text)
}

func (p Palette) wrap(code, text string) string {
	if !p.enabled || text == "" {
		return text
	}
	return code + text + resetCode
}

func useColor(w io.Writer) bool {
	if force := envpolicy.Get("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if envpolicy.Get("NO_COLOR") != "" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	term := envpolicy.Get("TERM")
	return term != "" && term != "dumb"
}

func Fprintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
