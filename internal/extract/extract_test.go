package extract

import (
	"testing"

	"docflow/internal/model"
)

// fieldValue returns one field's value, or "" if the field is absent.
func fieldValue(fields []model.Field, name string) string {
	for _, f := range fields {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func TestInvoice(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		field string
		want  string
	}{
		{
			name:  "invoice number",
			text:  "INVOICE\nInvoice No: INV-2026-0042\nTotal: 1,250.00",
			field: FieldInvoiceNumber,
			want:  "INV-2026-0042",
		},
		{name: "invoice number with a hash", text: "Invoice # A7781", field: FieldInvoiceNumber, want: "A7781"},
		{name: "invoice number spelled out", text: "Invoice Number 2026/0042", field: FieldInvoiceNumber, want: "2026/0042"},

		{name: "total", text: "Total: 1,250.00", field: FieldTotal, want: "125000"},
		{name: "total with a currency code", text: "Total USD 1,250.00", field: FieldTotal, want: "125000"},
		{name: "total with a symbol", text: "Total: $1,250.00", field: FieldTotal, want: "125000"},
		{name: "amount due", text: "Amount Due: 980.50", field: FieldTotal, want: "98050"},
		{
			// A subtotal earlier in the document must not win over the
			// grand total, which is the number anybody cares about.
			name:  "grand total beats an earlier subtotal",
			text:  "Subtotal: 1,000.00\nVAT (10%): 100.00\nGrand Total: 1,100.00",
			field: FieldTotal,
			want:  "110000",
		},
		{
			name:  "european separators",
			text:  "Total: 1.250,00",
			field: FieldTotal,
			want:  "125000",
		},

		{name: "vat", text: "VAT (10%): 100.00", field: FieldTax, want: "10000"},
		{name: "tax", text: "Tax: 87.25", field: FieldTax, want: "8725"},

		{name: "currency code", text: "Total VND 1250000", field: FieldCurrency, want: "VND"},
		{name: "date", text: "Invoice Date: 2026-10-16", field: FieldInvoiceDate, want: "2026-10-16"},
		{name: "date with slashes", text: "Date: 16/10/2026", field: FieldInvoiceDate, want: "16/10/2026"},

		// OCR breaks a label from its value as often as not.
		{
			name:  "label and value on separate lines",
			text:  "Invoice No:\nINV-9001",
			field: FieldInvoiceNumber,
			want:  "INV-9001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := For(model.DocInvoice, tt.text, 0.9)
			if got := fieldValue(fields, tt.field); got != tt.want {
				t.Errorf("%s = %q, want %q (fields: %+v)", tt.field, got, tt.want, fields)
			}
		})
	}
}

func TestIDDocument(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		field string
		want  string
	}{
		{name: "id number", text: "ID No: 012345678901", field: FieldDocumentNumber, want: "012345678901"},
		{name: "passport number", text: "Passport Number: P1234567", field: FieldDocumentNumber, want: "P1234567"},
		{name: "full name", text: "Full Name: Nguyen Van An", field: FieldFullName, want: "Nguyen Van An"},
		{name: "name", text: "Name: Mary Jane Watson", field: FieldFullName, want: "Mary Jane Watson"},
		{name: "date of birth", text: "Date of Birth: 1990-04-12", field: FieldDateOfBirth, want: "1990-04-12"},
		{name: "dob abbreviated", text: "D.O.B. 12/04/1990", field: FieldDateOfBirth, want: "12/04/1990"},
		{name: "expiry", text: "Date of Expiry: 2031-04-12", field: FieldExpiryDate, want: "2031-04-12"},
		{name: "valid until", text: "Valid Until 2031-04-12", field: FieldExpiryDate, want: "2031-04-12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := For(model.DocID, tt.text, 0.9)
			if got := fieldValue(fields, tt.field); got != tt.want {
				t.Errorf("%s = %q, want %q (fields: %+v)", tt.field, got, tt.want, fields)
			}
		})
	}
}

// Fewer fields is the right answer for text that does not carry them. A
// wrong total on an invoice is worse than no total.
func TestNothingIsInvented(t *testing.T) {
	tests := []struct {
		name    string
		docType model.DocType
		text    string
	}{
		{name: "empty", docType: model.DocInvoice, text: ""},
		{name: "unrelated prose", docType: model.DocInvoice, text: "The quick brown fox jumps over the lazy dog."},
		{name: "a number with no label", docType: model.DocInvoice, text: "1,250.00"},
		{name: "a date with no label", docType: model.DocID, text: "1990-04-12"},
		{name: "whitespace", docType: model.DocInvoice, text: "   \n\t  \n "},
		{name: "an invoice's text in an id document", docType: model.DocID, text: "Total: 1,250.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := For(tt.docType, tt.text, 0.9); len(got) != 0 {
				t.Errorf("For() = %+v, want nothing", got)
			}
		})
	}
}

func TestDocTypesWithoutPatterns(t *testing.T) {
	// A plain image is stored and recognized, but nothing is claimed about
	// its structure.
	if got := For(model.DocImage, "Total: 1,250.00", 0.9); len(got) != 0 {
		t.Errorf("For(image) = %+v, want nothing", got)
	}
	if got := For(model.DocType("receipt"), "Total: 1,250.00", 0.9); len(got) != 0 {
		t.Errorf("For(unknown) = %+v, want nothing", got)
	}
}

// A field is exactly as reliable as the characters it was read from, so this
// package reports the text's confidence rather than inventing one.
func TestConfidenceComesFromTheText(t *testing.T) {
	for _, confidence := range []float64{0.1, 0.55, 1.0} {
		fields := For(model.DocInvoice, "Invoice No: INV-1\nTotal: 10.00", confidence)
		if len(fields) == 0 {
			t.Fatal("no fields extracted")
		}
		for _, f := range fields {
			if f.Confidence != confidence {
				t.Errorf("field %s confidence = %v, want %v", f.Name, f.Confidence, confidence)
			}
		}
	}
}

// The result goes into model.DocumentResult, which rejects a duplicate field
// name and a confidence outside [0,1].
func TestResultIsAcceptableToTheModel(t *testing.T) {
	text := `INVOICE
Invoice No: INV-2026-0042
Invoice Date: 2026-10-16
Subtotal: 1,000.00
VAT (10%): 100.00
Grand Total: USD 1,100.00`

	fields := For(model.DocInvoice, text, 0.93)
	if len(fields) < 4 {
		t.Fatalf("extracted %d fields, want at least four: %+v", len(fields), fields)
	}

	result := model.DocumentResult{
		JobID:      "01J8ZQJ9X7K3M4N5P6Q7R8S9TV",
		Provider:   model.ProviderTesseract,
		Confidence: 0.93,
		Pages:      1,
		Text:       text,
		Fields:     fields,
	}
	if err := result.Validate(); err != nil {
		t.Errorf("the extracted fields are not storable: %v", err)
	}
}

func TestFieldOrderIsStable(t *testing.T) {
	text := "Invoice No: INV-1\nDate: 2026-01-02\nTotal: 10.00\nVAT: 1.00\nUSD"

	first := For(model.DocInvoice, text, 0.9)
	for range 5 {
		again := For(model.DocInvoice, text, 0.9)
		if len(again) != len(first) {
			t.Fatalf("field count varies between runs: %d then %d", len(first), len(again))
		}
		for i := range first {
			if again[i] != first[i] {
				t.Fatalf("field %d varies between runs: %+v then %+v", i, first[i], again[i])
			}
		}
	}
}

func TestCollapse(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "a b", want: "a b"},
		{in: "a\nb", want: "a b"},
		{in: "a  \t\n  b", want: "a b"},
		{in: "  padded  ", want: "padded"},
		{in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := collapse(tt.in); got != tt.want {
				t.Errorf("collapse(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanAmount(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "1,250.00", want: "125000"},
		{in: "1.250,00", want: "125000"},
		{in: "1 250 000", want: "1250000"},
		{in: "0.50", want: "50"},
		{in: "10", want: "10"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := cleanAmount(tt.in); got != tt.want {
				t.Errorf("cleanAmount(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
