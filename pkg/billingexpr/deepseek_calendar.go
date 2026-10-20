package billingexpr

import (
	_ "embed"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

//go:embed deepseek_calendar.json
var deepSeekCalendarJSON []byte

var deepSeekCalendar = func() map[string][]struct{ Start, End string } {
	var calendar struct {
		Years map[string][]struct{ Start, End string } `json:"years"`
	}
	if err := common.Unmarshal(deepSeekCalendarJSON, &calendar); err != nil {
		panic(fmt.Sprintf("invalid embedded DeepSeek holiday calendar: %v", err))
	}
	return calendar.Years
}()

var deepSeekUnknownYears sync.Map
var deepSeekBeijing = time.FixedZone("Asia/Shanghai", 8*60*60)

// DeepSeek defines every weekend as off-peak, including official make-up
// workdays. A missing year's holiday announcement must never raise a charge.
func deepSeekPeak(at time.Time) bool {
	local := at.In(deepSeekBeijing)
	year := strconv.Itoa(local.Year())
	holidays, known := deepSeekCalendar[year]
	if !known {
		if _, warned := deepSeekUnknownYears.LoadOrStore(year, true); !warned {
			common.SysError(common.LogText("DeepSeek holiday calendar does not cover %s; using off-peak pricing until the official calendar is added", year))
		}
		return false
	}
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return false
	}
	date := local.Format(time.DateOnly)
	for _, holiday := range holidays {
		if date >= holiday.Start && date <= holiday.End {
			return false
		}
	}
	hour := local.Hour()
	return (hour >= 9 && hour < 12) || (hour >= 14 && hour < 18)
}
