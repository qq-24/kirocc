package messages

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/d-kuro/kirocc/internal/kiroproto"
)

var dumpDir = os.Getenv("KIROCC_DUMP_DIR")
var dumpSeq atomic.Int64

func dumpPayload(payload *kiroproto.Payload, model string) {
	if dumpDir == "" {
		return
	}
	data, _ := json.Marshal(payload)
	seq := dumpSeq.Add(1)
	name := fmt.Sprintf("%s_%03d_%s.json", time.Now().Format("20060102_150405"), seq, model)
	_ = os.MkdirAll(dumpDir, 0o755)
	_ = os.WriteFile(filepath.Join(dumpDir, name), data, 0o644)
}
