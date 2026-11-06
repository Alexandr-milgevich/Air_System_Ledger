package xlsxledger

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"
	"montage-ledger/internal/calculation"
)

const MaxUpload = 2 << 20

var sysCode = regexp.MustCompile(`(?i)(?:^|[^А-ЯA-Z])([ПВ])-\s*(\d+)`)
var numericRow = regexp.MustCompile(`^\d+$`)

type Item struct {
	Row                                                                                        int
	SystemCode, SystemName, Name, Unit, Location                                               string
	Quantity, Coefficient, Calculated, Billable, ResultUnit, BillableUnit, Price, Amount, Mode string
	Warning                                                                                    string
}
type Preview struct {
	Sheet    string
	Items    []Item
	Warnings []string
	Total    string
}

// numeric normalizes Excel's stored numeric values into the decimal precision
// supported by our PostgreSQL columns. Excel may store e.g. displayed 0.289
// as 0.28899999999999998; those binary artifacts are not invalid coefficients.
// This normalization applies only to the XLSX importer, not user-entered amounts.
func numeric(v string, scale int) (string, error) {
	v = strings.TrimSpace(strings.ReplaceAll(v, "\u00a0", ""))
	if v == "" || v == "-" {
		return "", nil
	}
	v = strings.ReplaceAll(v, ",", ".")
	// Excel sometimes stores cached formula results in scientific notation.
	// Parsing with big.ParseFloat permits exponents without float64 conversion.
	// big.Rat.SetString also handles ordinary decimals exactly.
	var r *big.Rat
	if strings.ContainsAny(v, "eE") {
		if !scientificDecimal.MatchString(v) {
			return "", errors.New("invalid scientific decimal")
		}
		f, _, err := big.ParseFloat(v, 10, 256, big.ToNearestEven)
		if err != nil || f.Sign() < 0 {
			return "", errors.New("invalid scientific decimal")
		}
		r, _ = f.Rat(nil)
	} else {
		if !excelDecimal.MatchString(v) {
			return "", errors.New("invalid decimal")
		}
		var ok bool
		r, ok = new(big.Rat).SetString(v)
		if !ok || r.Sign() < 0 {
			return "", errors.New("invalid decimal")
		}
	}
	// Reject input that would not fit the existing NUMERIC(20, scale) column.
	fixed := calculation.Fixed(r, scale)
	intPart := strings.SplitN(fixed, ".", 2)[0]
	if len(intPart) > 20-scale {
		return "", errors.New("decimal exceeds database precision")
	}
	return fixed, nil
}

var excelDecimal = regexp.MustCompile(`^\+?\d+(?:\.\d+)?$`)
var scientificDecimal = regexp.MustCompile(`^\+?\d+(?:\.\d+)?[eE][+-]?\d{1,3}$`)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return new(big.Rat)
	}
	return r
}
func closeMoney(a, b string) bool {
	x := new(big.Rat).Sub(rat(a), rat(b))
	if x.Sign() < 0 {
		x.Neg(x)
	}
	return x.Cmp(big.NewRat(1, 100)) <= 0
}
func normUnit(s string) (string, error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	switch s {
	case "шт.", "шт", "pcs":
		return "pcs", nil
	case "м.пог.", "м.п.", "мпог.", "пог.м", "м.п", "м", "m":
		return "m", nil
	case "м²", "м2", "кв.м", "m2":
		return "m2", nil
	default:
		return "", fmt.Errorf("неизвестная единица %q", s)
	}
}
func cell(row []string, n int) string {
	if n >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[n])
}

// Parse analyzes cached values, never executes Excel formulas or macros.
// Original quantities and coefficients are kept as snapshots, including legacy rounding.
func Parse(data []byte) (Preview, error) {
	if len(data) == 0 || len(data) > MaxUpload {
		return Preview{}, errors.New("размер XLSX превышает 2 МБ")
	}
	f, e := excelize.OpenReader(bytes.NewReader(data))
	if e != nil {
		return Preview{}, fmt.Errorf("невозможно открыть XLSX: %w", e)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return Preview{}, errors.New("нет листов")
	}
	sheet := sheets[0]
	for _, s := range sheets {
		if s == "1 этап" {
			sheet = s
			break
		}
	}
	rows, e := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	if e != nil {
		return Preview{}, e
	}
	if len(rows) > 20000 {
		return Preview{}, errors.New("слишком много строк")
	}
	p := Preview{Sheet: sheet, Total: "0.00"}
	systemName, systemCode := "", ""
	total := new(big.Rat)
	for idx, row := range rows {
		a, b := cell(row, 0), cell(row, 1)
		if !numericRow.MatchString(a) {
			if m := sysCode.FindStringSubmatch(a); len(m) == 3 {
				systemCode = strings.ToUpper(m[1]) + "-" + m[2]
				systemName = a
			}
			continue
		}
		if b == "" {
			continue
		}
		if systemCode == "" {
			return Preview{}, fmt.Errorf("строка %d: не найден заголовок системы", idx+1)
		}
		if len(p.Items) >= 2000 {
			return Preview{}, errors.New("максимум 2000 позиций")
		}
		unit, e := normUnit(cell(row, 2))
		if e != nil {
			return Preview{}, fmt.Errorf("строка %d: %w", idx+1, e)
		}
		q, e := numeric(cell(row, 3), 8)
		if e != nil || q == "" {
			return Preview{}, fmt.Errorf("строка %d: неверное количество", idx+1)
		}
		coef, e := numeric(cell(row, 4), 10)
		if e != nil {
			return Preview{}, fmt.Errorf("строка %d: неверный коэффициент", idx+1)
		}
		area, e := numeric(cell(row, 5), 8)
		if e != nil {
			return Preview{}, fmt.Errorf("строка %d: неверная площадь", idx+1)
		}
		price, e := numeric(cell(row, 6), 4)
		if e != nil || price == "" {
			return Preview{}, fmt.Errorf("строка %d: неверная цена", idx+1)
		}
		amount, e := numeric(cell(row, 7), 2)
		if e != nil || amount == "" {
			return Preview{}, fmt.Errorf("строка %d: отсутствует рассчитанная сумма", idx+1)
		}
		item := Item{Row: idx + 1, SystemCode: systemCode, SystemName: systemName, Name: b, Unit: unit, Location: cell(row, 8), Quantity: q, Coefficient: coef, Price: price, Amount: amount}
		if len([]rune(item.Name)) > 250 || len([]rune(item.Location)) > 250 {
			return Preview{}, fmt.Errorf("строка %d: слишком длинное название", idx+1)
		}
		calcQty := new(big.Rat).Mul(rat(q), rat(price))
		if area != "" {
			item.Calculated = area
			item.ResultUnit = "m2"
			calcArea := new(big.Rat).Mul(rat(area), rat(price))
			if closeMoney(amount, calculation.Fixed(calcArea, 2)) {
				item.Mode = "coefficient"
				item.BillableUnit = "m2"
				item.Billable = area
			} else if closeMoney(amount, calculation.Fixed(calcQty, 2)) {
				item.Mode = "direct"
				item.BillableUnit = unit
				item.Billable = q
			} else {
				item.Mode = "legacy_snapshot"
				item.BillableUnit = "m2"
				item.Billable = "0.00000000"
				item.Warning = "сумма не совпадает с произведением количества или площади на цену; сумма сохранена из XLSX"
			}
		} else {
			item.Calculated = q
			item.ResultUnit = unit
			item.BillableUnit = unit
			item.Billable = q
			item.Mode = "direct"
			if !closeMoney(amount, calculation.Fixed(calcQty, 2)) {
				item.Mode = "legacy_snapshot"
				item.Warning = "сумма не совпадает с количеством × цену"
			}
		}
		if item.Mode == "legacy_snapshot" && item.Billable == "0.00000000" { // Preserve original price/amount without pretending a derived billable quantity.
			item.Billable = area
		}
		if item.Mode == "legacy_snapshot" {
			p.Warnings = append(p.Warnings, fmt.Sprintf("Строка %d: %s", idx+1, item.Warning))
		}
		// Preserve input-side coefficient as supplied (do not replace with geometric PI).
		if item.Coefficient == "" {
			item.Coefficient = ""
		}
		total.Add(total, rat(item.Amount))
		p.Items = append(p.Items, item)
	}
	if len(p.Items) == 0 {
		return Preview{}, errors.New("не найдены позиции в колонках A:I")
	}
	p.Total = calculation.Fixed(total, 2)
	return p, nil
}

// Export uses only server-stored snapshots; formulas are not trusted as a source of truth.
func Export(w io.Writer, items []Item) error {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Ведомость"
	f.SetSheetName("Sheet1", sheet)
	headers := []string{"№", "Наименование работ", "Ед.", "Кол-во", "Коэффициент", "Площадь / расч. кол-во", "Цена за ед.", "Итого", "Место монтажа"}
	for i, h := range headers {
		c, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheet, c, h); err != nil {
			return err
		}
	}
	hdrStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"203858"}}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "center"}})
	f.SetCellStyle(sheet, "A1", "I1", hdrStyle)
	f.SetRowHeight(sheet, 1, 35)
	widths := map[string]float64{"A": 8, "B": 47, "C": 12, "D": 13, "E": 14, "F": 22, "G": 17, "H": 17, "I": 32}
	for col, sz := range widths {
		f.SetColWidth(sheet, col, col, sz)
	}
	moneyStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 4})
	current := ""
	outRow := 2
	no := 0
	totals := new(big.Rat)
	systemTotal := new(big.Rat)
	writeSubtotal := func() error {
		if current == "" {
			return nil
		}
		if err := f.SetCellValue(sheet, fmt.Sprintf("B%d", outRow), "Итого по системе "+current); err != nil {
			return err
		}
		if err := f.SetCellValue(sheet, fmt.Sprintf("H%d", outRow), number(calculation.Fixed(systemTotal, 2))); err != nil {
			return err
		}
		f.SetCellStyle(sheet, fmt.Sprintf("G%d", outRow), fmt.Sprintf("H%d", outRow), moneyStyle)
		outRow++
		systemTotal = new(big.Rat)
		return nil
	}
	for _, it := range items {
		if current != it.SystemCode {
			if err := writeSubtotal(); err != nil {
				return err
			}
			current = it.SystemCode
			if err := f.SetCellValue(sheet, fmt.Sprintf("A%d", outRow), safeText(it.SystemName)); err != nil {
				return err
			}
			f.MergeCell(sheet, fmt.Sprintf("A%d", outRow), fmt.Sprintf("I%d", outRow))
			f.SetCellStyle(sheet, fmt.Sprintf("A%d", outRow), fmt.Sprintf("I%d", outRow), hdrStyle)
			outRow++
		}
		no++
		cells := []any{no, safeText(it.Name), displayUnit(it.Unit), number(it.Quantity), nil, number(it.Calculated), number(it.Price), number(it.Amount), safeText(it.Location)}
		if it.Coefficient != "" {
			cells[4] = number(it.Coefficient)
		}
		for j, v := range cells {
			if v == nil {
				continue
			}
			c, _ := excelize.CoordinatesToCellName(j+1, outRow)
			if err := f.SetCellValue(sheet, c, v); err != nil {
				return err
			}
		}
		f.SetCellStyle(sheet, fmt.Sprintf("G%d", outRow), fmt.Sprintf("H%d", outRow), moneyStyle)
		systemTotal.Add(systemTotal, rat(it.Amount))
		totals.Add(totals, rat(it.Amount))
		outRow++
	}
	if err := writeSubtotal(); err != nil {
		return err
	}
	f.SetCellValue(sheet, fmt.Sprintf("B%d", outRow), "Итого по ведомости")
	f.SetCellValue(sheet, fmt.Sprintf("H%d", outRow), number(calculation.Fixed(totals, 2)))
	f.SetCellStyle(sheet, fmt.Sprintf("H%d", outRow), fmt.Sprintf("H%d", outRow), moneyStyle)
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	return f.Write(w)
}
func displayUnit(u string) string {
	switch u {
	case "m":
		return "м. пог."
	case "pcs":
		return "шт."
	case "m2":
		return "м²"
	}
	return u
}
func safeText(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "=") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "@") {
		return "'" + s
	}
	return s
}
func number(s string) float64 { r := rat(s); f, _ := r.Float64(); return f } // Excel's numeric representation is IEEE754; database retains exact decimals.
