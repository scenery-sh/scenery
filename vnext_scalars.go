package scenery

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

var (
	decimalPattern  = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
	durationPattern = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)(ns|us|ms|s|m|h|d|w)`)
)

const maxDecimalScaleMagnitude int64 = 1_000_000

func ParseInt(value string) (Int, error) {
	var integer big.Int
	if value == "" || strings.HasPrefix(value, "+") || (len(value) > 1 && value[0] == '0') || strings.HasPrefix(value, "-0") {
		return Int{}, fmt.Errorf("invalid canonical integer %q", value)
	}
	if _, ok := integer.SetString(value, 10); !ok {
		return Int{}, fmt.Errorf("invalid integer %q", value)
	}
	return Int{Int: integer}, nil
}

func (value Int) MarshalJSON() ([]byte, error) { return json.Marshal(value.String()) }

func (value *Int) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("Scenery int must be a JSON string: %w", err)
	}
	parsed, err := ParseInt(text)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

func ParseDecimal(value string) (Decimal, error) {
	if !decimalPattern.MatchString(value) {
		return Decimal{}, fmt.Errorf("invalid decimal %q", value)
	}
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	var exponent int64
	if index := strings.IndexAny(unsigned, "eE"); index >= 0 {
		parsed, err := strconv.ParseInt(unsigned[index+1:], 10, 64)
		if err != nil {
			return Decimal{}, fmt.Errorf("decimal exponent is out of range")
		}
		exponent, unsigned = parsed, unsigned[:index]
	}
	var scale int64
	if index := strings.IndexByte(unsigned, '.'); index >= 0 {
		scale, unsigned = int64(len(unsigned)-index-1), unsigned[:index]+unsigned[index+1:]
	}
	unsigned = strings.TrimLeft(unsigned, "0")
	if unsigned == "" {
		return Decimal{}, nil
	}
	scale -= exponent
	for scale > 0 && strings.HasSuffix(unsigned, "0") {
		unsigned = strings.TrimSuffix(unsigned, "0")
		scale--
	}
	if scale < -maxDecimalScaleMagnitude || scale > maxDecimalScaleMagnitude || int64(len(unsigned)) > maxDecimalScaleMagnitude {
		return Decimal{}, fmt.Errorf("decimal magnitude exceeds supported exact representation")
	}
	if negative {
		unsigned = "-" + unsigned
	}
	var coefficient big.Int
	coefficient.SetString(unsigned, 10)
	return Decimal{Coefficient: coefficient, Scale: int32(scale)}, nil
}

func (value Decimal) String() string {
	digits := value.Coefficient.String()
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	if value.Scale < 0 {
		digits += strings.Repeat("0", -int(value.Scale))
		if negative && digits != "0" {
			return "-" + digits
		}
		return digits
	}
	if value.Scale == 0 {
		if negative && digits != "0" {
			return "-" + digits
		}
		return digits
	}
	scale := int(value.Scale)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	result := digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	if negative {
		result = "-" + result
	}
	return result
}

func (value Decimal) MarshalJSON() ([]byte, error) { return json.Marshal(value.String()) }

func (value *Decimal) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("Scenery decimal must be a JSON string: %w", err)
	}
	parsed, err := ParseDecimal(text)
	if err != nil || parsed.String() != text {
		return fmt.Errorf("invalid canonical decimal %q", text)
	}
	*value = parsed
	return nil
}

func ParseUUID(value string) (UUID, error) {
	if len(value) != 36 || value != strings.ToLower(value) || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", fmt.Errorf("invalid canonical UUID %q", value)
	}
	b, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil {
		return "", fmt.Errorf("invalid UUID %q", value)
	}
	if len(b) != 16 || b[8]&0xc0 != 0x80 {
		return "", fmt.Errorf("invalid UUID variant")
	}
	return UUID(value), nil
}

func (value UUID) MarshalJSON() ([]byte, error) {
	if _, err := ParseUUID(string(value)); err != nil {
		return nil, err
	}
	return json.Marshal(string(value))
}
func (value *UUID) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := ParseUUID(text)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

func ParseDate(value string) (Date, error) {
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", err
	}
	return Date(value), nil
}
func (value Date) MarshalJSON() ([]byte, error) {
	if _, err := ParseDate(string(value)); err != nil {
		return nil, err
	}
	return json.Marshal(string(value))
}
func (value *Date) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := ParseDate(text)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

func ParseDateTime(value string) (DateTime, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return DateTime{}, err
	}
	return DateTime(parsed.UTC()), nil
}
func (value DateTime) String() string               { return time.Time(value).UTC().Format(time.RFC3339Nano) }
func (value DateTime) MarshalJSON() ([]byte, error) { return json.Marshal(value.String()) }
func (value *DateTime) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := ParseDateTime(text)
	if err != nil || parsed.String() != text {
		return fmt.Errorf("invalid canonical datetime %q", text)
	}
	*value = parsed
	return nil
}

func ParseDuration(value string) (Duration, error) {
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	matches := durationPattern.FindAllStringSubmatchIndex(unsigned, -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	position := 0
	var total big.Int
	units := map[string]int64{"ns": 1, "us": 1_000, "ms": 1_000_000, "s": int64(time.Second), "m": int64(time.Minute), "h": int64(time.Hour), "d": int64(24 * time.Hour), "w": int64(7 * 24 * time.Hour)}
	for _, match := range matches {
		if match[0] != position {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		position = match[1]
		count := new(big.Rat)
		if _, ok := count.SetString(unsigned[match[2]:match[3]]); !ok {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		count.Mul(count, new(big.Rat).SetInt64(units[unsigned[match[4]:match[5]]]))
		if !count.IsInt() {
			return 0, fmt.Errorf("duration is not an exact nanosecond")
		}
		total.Add(&total, count.Num())
	}
	if position != len(unsigned) || !total.IsInt64() {
		return 0, fmt.Errorf("duration out of range")
	}
	result := time.Duration(total.Int64())
	if negative {
		result = -result
	}
	return Duration(result), nil
}

func (value Duration) String() string {
	duration := time.Duration(value)
	negative := duration < 0
	if negative {
		duration = -duration
	}
	days := duration / (24 * time.Hour)
	duration %= 24 * time.Hour
	hours := duration / time.Hour
	duration %= time.Hour
	minutes := duration / time.Minute
	duration %= time.Minute
	seconds := duration / time.Second
	nanos := duration % time.Second
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	b.WriteByte('P')
	if days > 0 {
		fmt.Fprintf(&b, "%dD", days)
	}
	if duration == 0 && days > 0 {
		return b.String()
	}
	b.WriteByte('T')
	if hours > 0 {
		fmt.Fprintf(&b, "%dH", hours)
	}
	if minutes > 0 {
		fmt.Fprintf(&b, "%dM", minutes)
	}
	if nanos > 0 {
		fmt.Fprintf(&b, "%d.%sS", seconds, strings.TrimRight(fmt.Sprintf("%09d", nanos), "0"))
		return b.String()
	}
	if seconds > 0 || (days == 0 && hours == 0 && minutes == 0) {
		fmt.Fprintf(&b, "%dS", seconds)
	}
	return b.String()
}
func (value Duration) MarshalJSON() ([]byte, error) { return json.Marshal(value.String()) }
func (value *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := decodeJSONDuration(text)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

func decodeJSONDuration(value string) (Duration, error) {
	original := value
	if !strings.HasPrefix(value, "P") && !strings.HasPrefix(value, "-P") {
		return 0, fmt.Errorf("invalid ISO 8601 duration %q", value)
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	if !strings.HasPrefix(value, "P") {
		return 0, fmt.Errorf("invalid ISO 8601 duration")
	}
	value = strings.TrimPrefix(value, "P")
	datePart, timePart, hasTime := strings.Cut(value, "T")
	var source strings.Builder
	if negative {
		source.WriteByte('-')
	}
	if datePart != "" {
		if !strings.HasSuffix(datePart, "D") || strings.ContainsAny(datePart, "YMW") {
			return 0, fmt.Errorf("duration calendar units are forbidden")
		}
		source.WriteString(strings.TrimSuffix(datePart, "D"))
		source.WriteByte('d')
	}
	if hasTime {
		for _, unit := range []struct{ marker, suffix string }{{"H", "h"}, {"M", "m"}, {"S", "s"}} {
			if index := strings.Index(timePart, unit.marker); index >= 0 {
				if index == 0 {
					return 0, fmt.Errorf("invalid ISO 8601 duration")
				}
				source.WriteString(timePart[:index])
				source.WriteString(unit.suffix)
				timePart = timePart[index+1:]
			}
		}
	}
	if timePart != "" || source.Len() == 0 || source.String() == "-" {
		return 0, fmt.Errorf("invalid ISO 8601 duration")
	}
	parsed, err := ParseDuration(source.String())
	if err != nil || parsed.String() != original {
		return 0, fmt.Errorf("invalid canonical ISO 8601 duration %q", original)
	}
	return parsed, nil
}

func ParseSize(value string) (Size, error) {
	units := []struct {
		suffix     string
		multiplier uint64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"TB", 1_000_000_000_000}, {"GB", 1_000_000_000}, {"MB", 1_000_000}, {"kB", 1_000}, {"B", 1}}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			count, err := strconv.ParseUint(strings.TrimSuffix(value, unit.suffix), 10, 64)
			if err != nil || count > ^uint64(0)/unit.multiplier {
				return 0, fmt.Errorf("invalid size %q", value)
			}
			return Size(count * unit.multiplier), nil
		}
	}
	return 0, fmt.Errorf("invalid size %q", value)
}
func (value Size) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(value), 10))
}
func (value *Size) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != text {
		return fmt.Errorf("invalid canonical size %q", text)
	}
	*value = Size(parsed)
	return nil
}

func ParseRelativePath(value string) (RelativePath, error) {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return "", fmt.Errorf("invalid relative path %q", value)
	}
	return RelativePath(value), nil
}

func ParseURL(value string) (URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return URL{}, fmt.Errorf("invalid absolute URL %q", value)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	rawHostname := parsed.Hostname()
	if strings.Contains(rawHostname, "%") {
		return URL{}, fmt.Errorf("IPv6 zones are not allowed")
	}
	hostname := ""
	if ip := net.ParseIP(rawHostname); ip != nil {
		hostname = strings.ToLower(ip.String())
	} else {
		hostname, err = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.StrictDomainName(true), idna.ValidateLabels(true), idna.BidiRule()).ToASCII(rawHostname)
		if err != nil {
			return URL{}, fmt.Errorf("invalid IDNA hostname: %w", err)
		}
		hostname = strings.ToLower(hostname)
	}
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		if strings.Contains(hostname, ":") {
			parsed.Host = "[" + hostname + "]"
		} else {
			parsed.Host = hostname
		}
	} else {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	escapedPath, err := canonicalURLPath(parsed.EscapedPath())
	if err != nil {
		return URL{}, err
	}
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return URL{}, err
	}
	parsed.Path = decodedPath
	parsed.RawPath = ""
	if escapedPath != parsed.EscapedPath() {
		parsed.RawPath = escapedPath
	}
	if parsed.Path == "/" && !strings.Contains(value, "/") {
		parsed.Path, parsed.RawPath = "", ""
	}
	parsed.RawQuery, err = canonicalURLComponent(parsed.RawQuery)
	if err != nil {
		return URL{}, err
	}
	escapedFragment, err := canonicalURLComponent(parsed.EscapedFragment())
	if err != nil {
		return URL{}, err
	}
	decodedFragment, err := url.PathUnescape(escapedFragment)
	if err != nil {
		return URL{}, err
	}
	parsed.Fragment, parsed.RawFragment = decodedFragment, ""
	if escapedFragment != parsed.EscapedFragment() {
		parsed.RawFragment = escapedFragment
	}
	return URL(*parsed), nil
}

func canonicalURLPath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	var b strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			b.WriteByte(value[index])
			continue
		}
		if index+2 >= len(value) {
			return "", fmt.Errorf("invalid URL percent escape")
		}
		decoded, err := strconv.ParseUint(value[index+1:index+3], 16, 8)
		if err != nil {
			return "", fmt.Errorf("invalid URL percent escape")
		}
		char := byte(decoded)
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~", rune(char)) {
			b.WriteByte(char)
		} else {
			fmt.Fprintf(&b, "%%%02X", char)
		}
		index += 2
	}
	canonical := b.String()
	trailingSlash := strings.HasSuffix(canonical, "/") || strings.HasSuffix(canonical, "/.") || strings.HasSuffix(canonical, "/..")
	segments := strings.Split(canonical, "/")
	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case ".":
			continue
		case "..":
			if len(clean) > 1 {
				clean = clean[:len(clean)-1]
			}
		default:
			clean = append(clean, segment)
		}
	}
	result := strings.Join(clean, "/")
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(result, "/") {
		result = "/" + result
	}
	if result == "" && strings.HasPrefix(value, "/") {
		result = "/"
	}
	if trailingSlash && !strings.HasSuffix(result, "/") {
		result += "/"
	}
	return result, nil
}

func canonicalURLComponent(value string) (string, error) {
	var b strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			if canonicalURLComponentByte(value[index]) {
				b.WriteByte(value[index])
			} else {
				fmt.Fprintf(&b, "%%%02X", value[index])
			}
			continue
		}
		if index+2 >= len(value) {
			return "", fmt.Errorf("invalid URL percent escape")
		}
		decoded, err := strconv.ParseUint(value[index+1:index+3], 16, 8)
		if err != nil {
			return "", fmt.Errorf("invalid URL percent escape")
		}
		char := byte(decoded)
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~", rune(char)) {
			b.WriteByte(char)
		} else {
			fmt.Fprintf(&b, "%%%02X", char)
		}
		index += 2
	}
	return b.String(), nil
}

func canonicalURLComponentByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' ||
		strings.ContainsRune("-._~!$&'()*+,;=:@/?", rune(value))
}
func (value URL) String() string               { parsed := url.URL(value); return parsed.String() }
func (value URL) MarshalJSON() ([]byte, error) { return json.Marshal(value.String()) }
func (value *URL) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := ParseURL(text)
	if err != nil || parsed.String() != text {
		return fmt.Errorf("invalid canonical URL %q", text)
	}
	*value = parsed
	return nil
}

func (value RelativePath) MarshalJSON() ([]byte, error) { return json.Marshal(string(value)) }
func (value *RelativePath) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := ParseRelativePath(text)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}
