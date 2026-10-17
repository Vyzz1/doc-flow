// Package extract turns recognized text into the structured fields a
// document was uploaded for.
//
// Extraction is deterministic and label-driven: a pattern finds a label and
// takes the value beside it. Nothing here guesses. A document whose labels
// are missing or misread yields fewer fields rather than wrong ones, because
// a wrong total on an invoice is worse than no total.
//
// The patterns are English. Recognition runs with the English language data,
// so there is nothing to match against in another language yet; adding one
// means adding its language data to the OCR image and its labels here.
package extract

import (
	"regexp"
	"strings"

	"docflow/internal/model"
)

// Field names this package produces. They are part of the API's response, so
// they are spelled once here rather than inline at each pattern.
const (
	FieldInvoiceNumber  = "invoiceNumber"
	FieldInvoiceDate    = "invoiceDate"
	FieldTotal          = "total"
	FieldTax            = "tax"
	FieldCurrency       = "currency"
	FieldDocumentNumber = "documentNumber"
	FieldFullName       = "fullName"
	FieldDateOfBirth    = "dateOfBirth"
	FieldExpiryDate     = "expiryDate"
)

// pattern finds one field in recognized text.
type pattern struct {
	name string
	re   *regexp.Regexp
	// clean normalizes the captured group. Without one the match is taken
	// as it stands, trimmed.
	clean func(string) string
}

// amount matches a number with optional thousands separators and decimals,
// written either way round: 1,250.00 and 1.250,00 both occur, and which is
// which depends on the issuer rather than on anything we can see.
const amount = `([0-9][0-9.,\x{00A0} ]*[0-9]|[0-9])`

// date matches the numeric forms that appear on documents. It deliberately
// does not try to parse them into a time: deciding whether 03/04/2026 is
// March or April needs a locale this text does not carry, and a wrong date
// is worse than the string the document actually shows.
const date = `([0-9]{1,4}[-/.][0-9]{1,2}[-/.][0-9]{2,4})`

var invoicePatterns = []pattern{
	{
		name:  FieldInvoiceNumber,
		re:    regexp.MustCompile(`(?i)invoice\s*(?:no\.?|number|#)\s*[:#]?\s*([A-Z0-9][A-Z0-9/-]{2,})`),
		clean: func(s string) string { return strings.ToUpper(strings.TrimRight(s, ".,")) },
	},
	{
		name: FieldInvoiceDate,
		re:   regexp.MustCompile(`(?i)(?:invoice\s+date|date\s+of\s+issue|date)\s*[:]?\s*` + date),
	},
	{
		// Checked before the plain total so that "total due" and "grand
		// total" win over a "subtotal" earlier in the document.
		name:  FieldTotal,
		re:    regexp.MustCompile(`(?i)(?:grand\s+total|total\s+due|amount\s+due|total\s+amount)\s*[:]?\s*(?:[A-Z]{3}\s*)?[$€£]?\s*` + amount),
		clean: cleanAmount,
	},
	{
		name:  FieldTotal,
		re:    regexp.MustCompile(`(?i)(?:^|[^a-z])total\s*[:]?\s*(?:[A-Z]{3}\s*)?[$€£]?\s*` + amount),
		clean: cleanAmount,
	},
	{
		name:  FieldTax,
		re:    regexp.MustCompile(`(?i)(?:vat|tax|gst)\s*(?:\([0-9.]+%\))?\s*[:]?\s*(?:[A-Z]{3}\s*)?[$€£]?\s*` + amount),
		clean: cleanAmount,
	},
	{
		name:  FieldCurrency,
		re:    regexp.MustCompile(`\b(USD|EUR|GBP|VND|JPY|AUD|CAD|SGD)\b`),
		clean: strings.ToUpper,
	},
}

var idPatterns = []pattern{
	{
		name:  FieldDocumentNumber,
		re:    regexp.MustCompile(`(?i)(?:id|passport|document|card)\s*(?:no\.?|number|#)\s*[:#]?\s*([A-Z0-9][A-Z0-9<-]{4,})`),
		clean: func(s string) string { return strings.ToUpper(strings.TrimRight(s, ".,")) },
	},
	{
		name: FieldFullName,
		re:   regexp.MustCompile(`(?i)(?:full\s+name|name)\s*[:]?\s*([A-Z][A-Za-z'\-]+(?:\s+[A-Z][A-Za-z'\-]+){1,4})`),
	},
	{
		name: FieldDateOfBirth,
		re:   regexp.MustCompile(`(?i)(?:date\s+of\s+birth|birth\s+date|d\.?o\.?b\.?)\s*[:]?\s*` + date),
	},
	{
		name: FieldExpiryDate,
		re:   regexp.MustCompile(`(?i)(?:date\s+of\s+expiry|expiry\s+date|expires?|valid\s+until)\s*[:]?\s*` + date),
	},
}

// cleanAmount strips the separators an amount may carry so the value is a
// bare number. Which separator meant what is not decided here: both are
// removed, and the digits are what the document showed.
func cleanAmount(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ',', '.', ' ', ' ':
			return -1
		default:
			return r
		}
	}, s)
	return strings.TrimLeft(s, "0")
}

// For returns the fields a document of this kind carries, in a stable order.
//
// confidence is the recognition confidence of the text these fields came
// out of, and becomes each field's confidence. This package adds no
// information to the text, so it claims none: a field is exactly as reliable
// as the characters it was read from.
func For(docType model.DocType, text string, confidence float64) []model.Field {
	var patterns []pattern
	switch docType {
	case model.DocInvoice:
		patterns = invoicePatterns
	case model.DocID:
		patterns = idPatterns
	case model.DocImage:
		// A plain image is stored and recognized, but nothing is claimed
		// about its structure.
		return nil
	default:
		return nil
	}
	return apply(patterns, text, confidence)
}

func apply(patterns []pattern, text string, confidence float64) []model.Field {
	// A line-collapsed copy, because OCR breaks a label and its value
	// across lines as often as not.
	flat := collapse(text)

	var fields []model.Field
	seen := map[string]bool{}
	for _, p := range patterns {
		// The first pattern for a name wins, which is what puts "grand
		// total" ahead of a "total" that might be a subtotal.
		if seen[p.name] {
			continue
		}
		match := p.re.FindStringSubmatch(flat)
		if len(match) < 2 {
			continue
		}
		value := strings.TrimSpace(match[1])
		if p.clean != nil {
			value = p.clean(value)
		}
		if value == "" {
			continue
		}
		seen[p.name] = true
		fields = append(fields, model.Field{Name: p.name, Value: value, Confidence: confidence})
	}
	return fields
}

// collapse turns runs of whitespace, including the line breaks OCR inserts
// mid-label, into single spaces.
var whitespace = regexp.MustCompile(`\s+`)

func collapse(text string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(text, " "))
}
