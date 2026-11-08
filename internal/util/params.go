package util

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func ParseTTL(s string) (time.Duration, error) {
	switch s {
	case "1h": return time.Hour, nil
	case "24h": return 24 * time.Hour, nil
	case "168h", "7d": return 168 * time.Hour, nil
	case "": return 24 * time.Hour, nil
	default:
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 || d > 7*24*time.Hour {
			return 0, fmt.Errorf("TTL must be between 1ns and 7 days")
		}
		return d, nil
	}
}

const maxHighlightLine = 10000

func ParseHL(s string) map[int]bool {
	hl := map[int]bool{}
	if s == "" { return hl }
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" { continue }
		if strings.Contains(part, "-") {
			ch := strings.SplitN(part, "-", 2)
			a, errA := strconv.Atoi(strings.TrimSpace(ch[0]))
			b, errB := strconv.Atoi(strings.TrimSpace(ch[1]))
			if errA == nil && errB == nil {
				if a>b { a,b=b,a }
				if a < 1 { a = 1 }
				if b > maxHighlightLine { b = maxHighlightLine }
				for i:=a; i<=b; i++ { hl[i]=true }
			}
		} else if n, err := strconv.Atoi(part); err == nil && n > 0 && n <= maxHighlightLine {
			hl[n] = true
		}
	}
	return hl
}

func IsTruthy(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s=="1" || s=="true" || s=="on" || s=="yes"
}

