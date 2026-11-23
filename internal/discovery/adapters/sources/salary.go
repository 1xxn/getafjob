package sources

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/1xxn/getafjob/internal/discovery"
)

// Salary is a yearly gross pay range as a posting states it; Currency is an ISO 4217 code or empty.
type Salary struct {
	Min, Max int
	Currency string
}

// maxYearly bounds a yearly amount; larger figures are typos or other units.
const maxYearly = 10_000_000

var (
	currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)
	// grouped is a number with thousands separators only: 72.000, 72,000 or 1.250.000.
	grouped = regexp.MustCompile(`^\d{1,3}([.,]\d{3})+$`)
)

// BaseSalary reads a schema.org baseSalary (a MonetaryAmount with a QuantitativeValue or a plain number) as a
// yearly range. Monthly amounts count twelve times; hourly, daily and weekly ones are dropped, the hours behind
// them are unknown. Without a unit only yearly-sized figures (10,000 and more) are taken.
func BaseSalary(v any) (Salary, bool) {
	bs, ok := v.(map[string]any)
	if !ok {
		if l, isList := v.([]any); isList && len(l) > 0 {
			return BaseSalary(l[0])
		}
		return Salary{}, false
	}
	unit := text(bs["unitText"])
	var lo, hi float64
	if q, ok := bs["value"].(map[string]any); ok {
		unit = or(text(q["unitText"]), unit)
		lo, hi = amount(q["minValue"]), amount(q["maxValue"])
		if exact := amount(q["value"]); exact > 0 && lo == 0 && hi == 0 {
			lo, hi = exact, exact
		}
	} else {
		lo = amount(bs["value"])
		hi = lo
	}
	return yearly(lo, hi, unit, text(bs["currency"]))
}

// yearly turns an amount range with its period into a yearly salary (see BaseSalary for the rules).
func yearly(lo, hi float64, unit, currency string) (Salary, bool) {
	s := Salary{Currency: strings.ToUpper(strings.TrimSpace(currency))}
	if !currencyCode.MatchString(s.Currency) {
		s.Currency = ""
	}
	switch strings.ToUpper(unit) {
	case "YEAR", "ANNUAL", "YEARLY":
	case "MONTH", "MONTHLY":
		lo, hi = lo*12, hi*12
	case "":
		if max(lo, hi) < 10_000 {
			return Salary{}, false
		}
	default: // HOUR, DAY, WEEK and anything else
		return Salary{}, false
	}
	if lo <= 0 {
		lo = hi
	}
	if hi <= 0 {
		hi = lo
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo <= 0 || hi > maxYearly {
		return Salary{}, false
	}
	s.Min, s.Max = int(math.Round(lo)), int(math.Round(hi))
	return s, true
}

// amount reads a number from JSON (number or text with thousands separators or a decimal comma).
func amount(v any) float64 {
	var s string
	switch x := v.(type) {
	case json.Number:
		s = x.String()
	case float64:
		return x
	case string:
		s = strings.ReplaceAll(strings.TrimSpace(x), " ", "")
		switch {
		case grouped.MatchString(s):
			s = strings.NewReplacer(".", "", ",", "").Replace(s)
		case strings.Contains(s, ",") && strings.Contains(s, "."):
			// Both: the later one is the decimal point ("72,500.50", "72.500,50").
			if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
				s = strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
			} else {
				s = strings.ReplaceAll(s, ",", "")
			}
		default:
			s = strings.ReplaceAll(s, ",", ".")
		}
	default:
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	}
	return f
}

// apply sets the posting's salary unless it has one.
func (s Salary) apply(p *discovery.Posting) {
	if s.Max == 0 || p.SalaryMin > 0 || p.SalaryMax > 0 {
		return
	}
	p.SalaryMin, p.SalaryMax, p.SalaryCurrency = s.Min, s.Max, s.Currency
}
