package profile_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	importprofile "github.com/kcl1104/geminus/internal/import/profile"
)

// A file at D-007's limits: 100,000 rows of synthetic CSV, about 50 MB
// (the upload limit). Not a performance claim (T-037): it shows what one
// profile costs on the machine it runs on.
func BenchmarkReadCSVAtTheLimits(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf案件編號,階段,開始時間,耗時毫秒,資源,結果,備註\r\n")
	pad := bytes.Repeat([]byte("說明"), 64)
	for i := range 100_000 {
		fmt.Fprintf(&buf, "CASE-%06d,檢驗,2026-10-15T08:%02d:%02d+08:00,%d,工站-%d,通過,%s\r\n", i, i/60%60, i%60, i*13%7200000, i%17, pad)
	}
	data := buf.Bytes()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		res, err := importprofile.Read(context.Background(), bytes.NewReader(data), importprofile.CSV, importprofile.DefaultLimits)
		if err != nil || res.Profile.Rows != 100_000 {
			b.Fatalf("%d rows: %v", res.Profile.Rows, err)
		}
	}
}
