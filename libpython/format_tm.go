package libpython

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func formatTM(formatRunes []rune, tmv *Ttm) string {
	var out strings.Builder
	weekdays := [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	months := [...]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	zoneName, zoneOffset := tmZone(tmv)
	date := time.Date(int(tmv.Ftm_year)+1900, time.Month(tmv.Ftm_mon)+1, int(tmv.Ftm_mday), int(tmv.Ftm_hour), int(tmv.Ftm_min), int(tmv.Ftm_sec), 0, time.FixedZone(zoneName, zoneOffset))
	isoYear, isoWeek := date.ISOWeek()
	for i := 0; i < len(formatRunes); {
		if formatRunes[i] != '%' || i+1 >= len(formatRunes) {
			out.WriteRune(formatRunes[i])
			i++
			continue
		}
		i++
		flag := rune(0)
		if i < len(formatRunes) && strings.ContainsRune("-_0^#", formatRunes[i]) {
			flag = formatRunes[i]
			i++
		}
		for i < len(formatRunes) && (formatRunes[i] == 'E' || formatRunes[i] == 'O') {
			i++
		}
		width := 0
		for i < len(formatRunes) && formatRunes[i] >= '0' && formatRunes[i] <= '9' {
			width = width*10 + int(formatRunes[i]-'0')
			i++
		}
		if i >= len(formatRunes) {
			out.WriteByte('%')
			break
		}
		verb := formatRunes[i]
		var value string
		numeric := func(v int64, defaultWidth int, defaultPad byte) {
			w, pad := defaultWidth, defaultPad
			if width != 0 {
				w = width
			}
			switch flag {
			case '-':
				w = 0
			case '_':
				pad = ' '
			case '0':
				pad = '0'
			}
			value = strconv.FormatInt(v, 10)
			if len(value) < w {
				value = strings.Repeat(string(pad), w-len(value)) + value
			}
		}
		switch verb {
		case '%':
			value = "%"
		case 'a':
			value = weekdays[tmv.Ftm_wday][:3]
		case 'A':
			value = weekdays[tmv.Ftm_wday]
		case 'b', 'h':
			value = months[tmv.Ftm_mon][:3]
		case 'B':
			value = months[tmv.Ftm_mon]
		case 'c':
			value = formatTM([]rune("%a %b %e %T %Y"), tmv)
		case 'C':
			numeric(int64((tmv.Ftm_year+1900)/100), 2, '0')
		case 'd':
			numeric(int64(tmv.Ftm_mday), 2, '0')
		case 'e':
			numeric(int64(tmv.Ftm_mday), 2, ' ')
		case 'D':
			value = formatTM([]rune("%m/%d/%y"), tmv)
		case 'F':
			value = formatTM([]rune("%Y-%m-%d"), tmv)
		case 'g':
			numeric(int64(isoYear%100), 2, '0')
		case 'G':
			numeric(int64(isoYear), 4, '0')
		case 'H':
			numeric(int64(tmv.Ftm_hour), 2, '0')
		case 'I':
			hour := tmv.Ftm_hour % 12
			if hour == 0 {
				hour = 12
			}
			numeric(int64(hour), 2, '0')
		case 'k':
			numeric(int64(tmv.Ftm_hour), 2, ' ')
		case 'w':
			numeric(int64(tmv.Ftm_wday), 1, '0')
		case 'u':
			day := tmv.Ftm_wday
			if day == 0 {
				day = 7
			}
			numeric(int64(day), 1, '0')
		case 'j':
			numeric(int64(tmv.Ftm_yday+1), 3, '0')
		case 'm':
			numeric(int64(tmv.Ftm_mon+1), 2, '0')
		case 'M':
			numeric(int64(tmv.Ftm_min), 2, '0')
		case 'n':
			value = "\n"
		case 'p':
			if tmv.Ftm_hour < 12 {
				value = "AM"
			} else {
				value = "PM"
			}
		case 'r':
			value = formatTM([]rune("%I:%M:%S %p"), tmv)
		case 'R':
			value = formatTM([]rune("%H:%M"), tmv)
		case 'S':
			numeric(int64(tmv.Ftm_sec), 2, '0')
		case 's':
			value = strconv.FormatInt(date.Unix(), 10)
		case 't':
			value = "\t"
		case 'T', 'X':
			value = formatTM([]rune("%H:%M:%S"), tmv)
		case 'U':
			numeric(int64((tmv.Ftm_yday+7-tmv.Ftm_wday)/7), 2, '0')
		case 'V':
			numeric(int64(isoWeek), 2, '0')
		case 'W':
			mondayWday := (tmv.Ftm_wday + 6) % 7
			numeric(int64((tmv.Ftm_yday+7-mondayWday)/7), 2, '0')
		case 'x':
			value = formatTM([]rune("%m/%d/%y"), tmv)
		case 'y':
			numeric(int64((tmv.Ftm_year+1900)%100), 2, '0')
		case 'Y':
			numeric(int64(tmv.Ftm_year+1900), 4, '0')
		case 'z':
			offset := int64(zoneOffset)
			sign := '+'
			if offset < 0 {
				sign = '-'
				offset = -offset
			}
			value = fmt.Sprintf("%c%02d%02d", sign, offset/3600, offset%3600/60)
		case 'Z':
			value = zoneName
		default:
			value = "%" + string(verb)
		}
		if flag == '^' {
			value = strings.ToUpper(value)
		} else if flag == '#' {
			var swapped strings.Builder
			for _, r := range value {
				if strings.ToUpper(string(r)) == string(r) {
					swapped.WriteString(strings.ToLower(string(r)))
				} else {
					swapped.WriteString(strings.ToUpper(string(r)))
				}
			}
			value = swapped.String()
		}
		out.WriteString(value)
		i++
	}
	return out.String()
}
