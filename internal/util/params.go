package util

import (
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
	default: return time.ParseDuration(s)
	}
}

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
				for i:=a;i<=b;i++ { hl[i]=true }
			}
		} else if n, err := strconv.Atoi(part); err == nil {
			hl[n] = true
		}
	}
	return hl
}

func IsTruthy(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s=="1" || s=="true" || s=="on" || s=="yes"
}

