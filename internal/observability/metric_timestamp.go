package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Convert an already decoded JSON number without floating-point rounding or
// exponent-sized allocation. Check the exact range before truncating sub-nanos.
func metricTimestampNanos(number json.Number) (int64, error) {
	reject := func() (int64, error) {
		return 0, fmt.Errorf("VictoriaMetrics sample timestamp is not a representable nanosecond time")
	}
	text := string(number)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	exponentText := "0"
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponentText, text = text[index+1:], text[:index]
	}
	fractionDigits := 0
	if index := strings.IndexByte(text, '.'); index >= 0 {
		fractionDigits = len(text) - index - 1
		text = text[:index] + text[index+1:]
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		return 0, nil
	}
	exponent, err := strconv.ParseInt(exponentText, 10, 64)
	if err != nil {
		if strings.HasPrefix(exponentText, "-") {
			return 0, nil
		}
		return reject()
	}
	// Leading zeros are gone. The nanosecond integer has base+exponent
	// digits; compare before adding so even int64-scale exponents cannot wrap.
	base := int64(len(text)) - int64(fractionDigits) + 9
	if exponent > 19-base {
		return reject()
	}
	if exponent <= -base {
		return 0, nil
	}
	digits := int(base + exponent)
	whole := text[:min(digits, len(text))]
	if digits > len(text) {
		whole += strings.Repeat("0", digits-len(text))
	}
	magnitude, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		return reject()
	}
	limit := uint64(math.MaxInt64)
	if negative {
		limit++
	}
	if magnitude > limit || (magnitude == limit && digits < len(text) && strings.Trim(text[digits:], "0") != "") {
		return reject()
	}
	if negative && magnitude == 1<<63 {
		return math.MinInt64, nil
	}
	value := int64(magnitude)
	if negative {
		value = -value
	}
	return value, nil
}
