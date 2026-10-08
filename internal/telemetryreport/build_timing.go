package telemetryreport

import (
	"encoding/json"
	"strings"
)

// Supervisor timing is floating point until repeated steps have been summed.
// The upper bound is exclusive: float64(MaxInt64) rounds up to 2^63.
func validSupervisorDuration(ms float64) bool {
	return ms >= 0 && ms < 0x1p63
}

type supervisorTiming struct {
	ms      float64
	invalid bool
}

// Decode timing separately so malformed timing types never partially decode
// otherwise valid identity/outcome fields. Missing/null timing is unavailable.
func decodeSupervisorTiming(raw json.RawMessage) supervisorTiming {
	timing := supervisorTiming{invalid: true}
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return timing
	}
	// A mathematically negative token can underflow float64 to negative zero.
	// Inspect only the mantissa; exponent magnitude causes no allocation.
	if raw[0] == '-' {
		mantissa := string(raw[1:])
		if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
			mantissa = mantissa[:index]
		}
		if strings.ContainsAny(mantissa, "123456789") {
			return timing
		}
	}
	if json.Unmarshal(raw, &timing.ms) == nil && validSupervisorDuration(timing.ms) {
		timing.invalid = false
	}
	return timing
}

// add retains a poisoned step cohort instead of mistaking its valid remainder
// for a complete timing. Each malformed input or overflowing addition is false.
func (s *supervisorTiming) add(value supervisorTiming) bool {
	if value.invalid {
		s.invalid = true
		return false
	}
	if s.invalid {
		return true
	}
	if !validSupervisorDuration(s.ms + value.ms) {
		s.invalid = true
		return false
	}
	s.ms += value.ms
	return true
}

func (s supervisorTiming) record(acc *timingAccumulator, ok bool) {
	if s.invalid {
		acc.addUntimed(ok)
		return
	}
	acc.add(int64(s.ms), ok)
}
