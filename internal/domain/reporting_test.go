package domain

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
)

func TestValidateReportWindow(t *testing.T) {
	s, e, err := ValidateReportWindow("2024-02-28", "2024-03-02")
	if err != nil {
		t.Fatalf("unexpected error for leap year window: %v", err)
	}
	if got := int(e.Sub(s).Hours() / 24); got != 3 {
		t.Fatalf("expected 3 days across leap year boundary, got %d", got)
	}

	if _, _, err := ValidateReportWindow("2025-05-10", "2025-05-10"); err == nil {
		t.Fatal("expected error when end_date == start_date")
	}
	if _, _, err := ValidateReportWindow("2025-01-01", "2026-06-01"); err == nil {
		t.Fatal("expected error when date range exceeds 366 days")
	}
}

func TestSafeDenominatorMath(t *testing.T) {
	bps, pct := SafeBPS(0, 0)
	if bps != nil || !strings.Contains(pct, "zero denominator") {
		t.Fatalf("expected safe nil BPS on zero denominator, got %v / %q", bps, pct)
	}
	val, formatted := SafeDividePence(10000, 0, "GBP")
	if val != nil || formatted != "N/A" {
		t.Fatalf("expected safe nil pence on zero denominator, got %v / %q", val, formatted)
	}
	bps, pct = SafeBPS(3, 4)
	if bps == nil || *bps != 7500 || pct != "75.00%" {
		t.Fatalf("unexpected 3/4 bps: %v %q", bps, pct)
	}
	val, formatted = SafeDividePence(25000, 2, "EUR")
	if val == nil || *val != 12500 || formatted != "EUR 125.00" {
		t.Fatalf("unexpected EUR divide: %v %q", val, formatted)
	}
}

func TestRenderAccountingCSVFormulaAndUnicodeSafety(t *testing.T) {
	export := AccountingExportResult{
		SchemaVersion: AccountingExportSchemaVer,
		ExportType:    ReportExportRefundLedger,
		Headers:       []string{"refund_id", "reason", "amount_pence"},
		Rows: []map[string]string{
			{
				"refund_id":    "=cmd|' /C calc'!A0",
				"reason":       "  +SUM(A1:A2)\nline2 \"quoted\", café £",
				"amount_pence": "-2500",
			},
			{
				"refund_id":    "@HYPERLINK(\"http://evil\")",
				"reason":       "\t=2+2",
				"amount_pence": "12000",
			},
			{
				"refund_id":    "normal-id",
				"reason":       "-evil()",
				"amount_pence": "-malicious()",
			},
		},
	}

	raw, err := RenderAccountingCSV(export)
	if err != nil {
		t.Fatalf("RenderAccountingCSV failed: %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("RFC4180 CSV re-parse failed: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("expected 4 CSV rows including header, got %d", len(records))
	}
	if records[1][0] != "'=cmd|' /C calc'!A0" {
		t.Fatalf("expected leading = neutralized, got %q", records[1][0])
	}
	if !strings.HasPrefix(records[1][1], "'") || !strings.Contains(records[1][1], "café £") {
		t.Fatalf("expected leading whitespace/+ formula neutralized with unicode preserved, got %q", records[1][1])
	}
	if records[1][2] != "-2500" {
		t.Fatalf("expected legitimate signed numeric column preserved, got %q", records[1][2])
	}
	if records[2][0][0] != '\'' || records[2][1][0] != '\'' {
		t.Fatalf("expected @ and tab triggers neutralized, got %q / %q", records[2][0], records[2][1])
	}
	if records[3][2] != "'-malicious()" {
		t.Fatalf("expected non-numeric expression in numeric column neutralized, got %q", records[3][2])
	}
}
