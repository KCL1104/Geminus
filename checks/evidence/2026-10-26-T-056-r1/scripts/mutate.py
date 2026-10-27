#!/usr/bin/env python3
"""T-056 review round 1: mutants of the fixes and the photo smoke.

Each mutant changes one place of the copy in src/ (git archive of the
branch), runs the named tests, and restores the file byte for byte. A build
failure is INVALID, never killed. DB mutants run on dt_test_t056r1m
(migrated by make db-test) with GEMINUS_REQUIRE_DB_TESTS=1. Every AI
variable is unset; no provider is called (the tests use fakes and local
test servers only).
"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(HERE, "src")
GO = "/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin/go"
DB = "postgres://postgres@/dt_test_t056r1m?host=/var/lib/postgresql/geminus-throwaway-hkb6&port=55450"

AI_VARS = ["AI_PROVIDER", "AI_POLICY_APPROVAL", "AI_MODEL", "GEMINUS_AI_LIVE", "AI_VISION_PROVIDER", "AI_VISION_POLICY_APPROVAL",
           "AI_VISION_MODEL", "GEMINUS_MUSE_API_KEY", "GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "MODEL_API_KEY",
           "ANTHROPIC_API_KEY", "AI_EVAL_LIVE_MAX_USD"]

IMG = "internal/proposals/image.go"
PH = "internal/proposals/photo/photo.go"
HTTPF = "internal/proposals/image_http.go"
REV = "internal/ai/review/photo.go"
PIPE = "internal/ai/photopipeline/photopipeline.go"
EVP = "internal/aievals/photo.go"
EVM = "internal/aievals/main.go"
EVL = "internal/aievals/live.go"
META = "internal/ai/provider/meta/meta.go"

DBAPI = (["./internal/api/"], "^(TestPhotoIdempotency|TestPhotoReplayAfterPublish|TestPhotoConcurrentSameKey|TestPhotoRefusals|TestPhotoStaleBaseAndAvailability)$", True)
PROPS = (["./internal/proposals/"], "TestImageKeys|TestRefused", False)
PHOTO = (["./internal/proposals/photo/"], "", False)
REVIEW = (["./internal/ai/review/"], "TestPhoto", False)
GEN = (["./internal/proposals/generator/", "./internal/ai/photopipeline/"], "", False)
SMOKE = (["./internal/aievals/", "./internal/config/aiapproval/", "./internal/ai/provider/meta/", "./cmd/ai-eval-live/"], "Photo|Count", False)

MUTANTS = [
    # M1: the key before the base.
    ("K1 base checked before the key (the old order)", IMG,
     """		var err error
		if seen, err = idempotency.Seen(ctx, tx, k); err != nil || seen {
			return err
		}
		_, _, err = checkBase(ctx, tx, m.OrganizationID, modelID, f.BaseVersionID, false)
		return err""",
     """		if _, _, err := checkBase(ctx, tx, m.OrganizationID, modelID, f.BaseVersionID, false); err != nil {
			return err
		}
		var err error
		seen, err = idempotency.Seen(ctx, tx, k)
		return err""", DBAPI),
    ("K2 the base checked for a used key too", IMG,
     "if seen, err = idempotency.Seen(ctx, tx, k); err != nil || seen {",
     "if seen, err = idempotency.Seen(ctx, tx, k); err != nil {", DBAPI),
    # L4: one store per key.
    ("K3 requests with one key do not wait for each other", IMG,
     """	release, err := imageKeys.lock(ctx, k)
	if err != nil {
		return 0, nil, err
	}
	defer release()""",
     """	release := func() {}
	defer release()""", DBAPI),
    ("K4 a replay decodes and stores the file again", IMG, "	if !seen {\n\t\tim, err := photo.Sanitize(ctx, data)",
     "	if true || !seen {\n\t\tim, err := photo.Sanitize(ctx, data)", DBAPI),
    ("K5 the lock table keeps released keys", IMG, """		if e.waiting--; e.waiting == 0 {
			delete(l.held, k)
		}""", """		e.waiting--""", PROPS),
    ("K6 a request that gave up waiting stays counted", IMG, """	case <-ctx.Done():
		done()
		return nil, ctx.Err()""", """	case <-ctx.Done():
		return nil, ctx.Err()""", PROPS),
    ("K7 the lock never waits", IMG, """	select {
	case e.slot <- struct{}{}:
		return func() { <-e.slot; done() }, nil""", """	select {
	case e.slot <- struct{}{}:
		return func() { <-e.slot; done() }, nil
	default:
		return func() { done() }, nil""", PROPS),
    # M2: the decode bound.
    ("P1 no header inspection", PH, """	if err := inspect(data, mediaType); err != nil {
		return Image{}, err
	}""", "", PHOTO),
    ("P2 16-bit PNG accepted", PH, "depth > 8 || interlace != 0", "depth > 16 || interlace != 0", PHOTO),
    ("P3 interlaced PNG accepted", PH, "depth > 8 || interlace != 0", "depth > 8 || (interlace != 0 && false)", PHOTO),
    ("P4 other SOF markers not refused", PH, """		case m >= markerSOF0 && m <= markerSOF15 && m != markerDHT && m != markerJPG && m != markerDAC:
			return ErrEncoding // progressive, lossless, arithmetic-coded or hierarchical""", "", PHOTO),
    ("P5 4 components accepted", PH, "if seg[0] != 8 || (nf != 1 && nf != 3) {", "if seg[0] != 8 || (nf != 1 && nf != 3 && nf != 4) {", PHOTO),
    ("P6 any precision accepted", PH, "if seg[0] != 8 || (nf != 1 && nf != 3) {", "if nf != 1 && nf != 3 {", PHOTO),
    ("P7 the Adobe transform not read", PH, """			if len(seg) >= 12 && string(seg[:5]) == "Adobe" && seg[11] == 0 {
				rgb = true
			}""", "", PHOTO),
    ("P8 the RGB component IDs not read", PH, "if rgb || (len(ids) == 3 && ids[0] == 'R' && ids[1] == 'G' && ids[2] == 'B') {", "if rgb {", PHOTO),
    ("P9 a component in two scans accepted", PH, """				if scanned[id] {
					return ErrEncoding
				}""", "", PHOTO),
    ("P10 the walk stops at the first scan", PH, """			// The entropy-coded data, up to the next marker: 0xFF 0x00 is a""",
     """			if rgb || (len(ids) == 3 && ids[0] == 'R' && ids[1] == 'G' && ids[2] == 'B') {
				return ErrEncoding
			}
			return nil
			// The entropy-coded data, up to the next marker: 0xFF 0x00 is a""", PHOTO),
    ("P11 the re-encoding not cut at MaxBytes", PH, """	if len(p) > c.max-len(c.buf) {
		return 0, ErrTooLarge
	}""", "", PHOTO),
    ("P12 three decode slots", PH, "var decodeSlots = make(chan struct{}, 2)", "var decodeSlots = make(chan struct{}, 3)", PHOTO),
    ("P13 the header inspected after the slot", PH, """	if err := inspect(data, mediaType); err != nil {
		return Image{}, err
	}
	cfg, err := config(data, mediaType)
	if err != nil {
		return Image{}, err
	}
	select {
	case decodeSlots <- struct{}{}:
		defer func() { <-decodeSlots }()
	case <-ctx.Done():
		return Image{}, ctx.Err()
	}""", """	cfg, err := config(data, mediaType)
	if err != nil {
		return Image{}, err
	}
	select {
	case decodeSlots <- struct{}{}:
		defer func() { <-decodeSlots }()
	case <-ctx.Done():
		return Image{}, ctx.Err()
	}
	if err := inspect(data, mediaType); err != nil {
		return Image{}, err
	}""", PHOTO),
    ("P14 bytes that are no marker refused", PH, """		for i < len(data) && data[i] != 0xff {
			i++
		}
		for i < len(data) && data[i] == 0xff {""", """		if i < len(data) && data[i] != 0xff {
			return ErrInvalid
		}
		for i < len(data) && data[i] == 0xff {""", PHOTO),
    ("P15 a larger documented bound", PH, "MaxDecodeBytes = 80 << 20", "MaxDecodeBytes = 1 << 30", PHOTO),
    ("P16 the decode bound below the measured", PH, "MaxDecodeBytes = 80 << 20", "MaxDecodeBytes = 60 << 20", PHOTO),
    ("H1 ErrEncoding mapped as an invalid image", HTTPF, "case errors.Is(err, photo.ErrEncoding):", "case errors.Is(err, photo.ErrEncoding) && false:", PROPS),
    # L1: every number.
    ("R1 domain_attributes not read", REV, """		if vs, ok := attributeNumbers(candidate.Nodes[i].DomainAttributes); !ok || unknown(vs) {""",
     """		if vs, ok := attributeNumbers(candidate.Nodes[i].DomainAttributes); false && (!ok || unknown(vs)) {""", REVIEW),
    ("R2 metric periods not read", REV, "if m.Period != nil && !known[*m.Period] {", "if false && m.Period != nil && !known[*m.Period] {", REVIEW),
    ("R3 the base's attribute numbers not known", REV, """		vs, _ := attributeNumbers(n.DomainAttributes)
		for _, v := range vs {
			known[v] = true
		}""", """		_ = n""", REVIEW),
    ("R4 the base's metric periods not known", REV, """		if m.Period != nil {
			known[*m.Period] = true
		}""", "		_ = m", REVIEW),
    ("R5 calendars not read", REV, """	if unknown(values(&model.ModelIR{Calendars: candidate.Calendars})) {""",
     """	if false && unknown(values(&model.ModelIR{Calendars: candidate.Calendars})) {""", REVIEW),
    ("R6 an unreadable number taken", REV, "if vs, ok := attributeNumbers(candidate.Nodes[i].DomainAttributes); !ok || unknown(vs) {",
     "if vs, _ := attributeNumbers(candidate.Nodes[i].DomainAttributes); unknown(vs) {", REVIEW),
    ("R7 an out-of-range number taken", REV, """		if err != nil {
			return nil, false
		}
		out = append(out, v)""", """		_ = err
		out = append(out, v)""", REVIEW),
    # The shared pipeline.
    ("G1 no photo rules", PIPE, "outcome = outcome.Refuse(review.Photo(req.Base.IR, outcome.Candidate, len(seg.Segments)))", "_ = seg", GEN),
    ("G2 a turned frame taken", PIPE, "case seg.Width != req.Image.Width || seg.Height != req.Image.Height:", "case false:", GEN),
    ("G3 no box still asks the vision model", PIPE, """	case len(seg.Segments) == 0:
		r.Outcome = noBoxes(req.OutputLocale)
		return r, nil""", "", GEN),
    ("G4 the segmenter's metadata lost on its failure", PIPE, "r.Segmenter, r.Segments = &seg.Metadata, len(seg.Segments)",
     "if err == nil {\n\t\tr.Segmenter, r.Segments = &seg.Metadata, len(seg.Segments)\n\t}", GEN),
    ("G5 the vision call's metadata lost", PIPE, "	r.Vision = &meta\n", "", GEN),
    # The smoke.
    ("S1 the count not recorded", META, "contextWindowTokens), Counted: n}, nil", "contextWindowTokens)}, nil", SMOKE),
    ("S2 the quote's count not kept", EVP, "rep.Judge.Counted, rep.Judge.ReservedInput = q.Counted, q.MaxInputTokens",
     "rep.Judge.ReservedInput = q.MaxInputTokens", SMOKE),
    ("S3 the judge not asked first", EVP, """	if !photopipeline.TakesImages(r.Judge) {
		rep.Status, rep.Code, rep.Gates[GatePipelineFailed] = "failed", "AI_IMAGE_UNSUPPORTED", 1""",
     """	if false {
		rep.Status, rep.Code, rep.Gates[GatePipelineFailed] = "failed", "AI_IMAGE_UNSUPPORTED", 1""", SMOKE),
    ("S4 the photo-path gate not asked", EVM, "	if !v.LiveEnabled() {\n\t\twhy := v.LiveOff", "	if false {\n\t\twhy := v.LiveOff", SMOKE),
    ("S5 the day's remainder not checked", EVM, "	case capNano > ai.Limits.Day-day:\n\t\twhy = ", "	case false:\n\t\twhy = ", SMOKE),
    ("S6 the run's line not closed", EVM, """	if err := closeRun(result); err != nil {
		fmt.Fprintln(stdout, summary()+"; report "+path)
		providerError()""", """	if err := func(string) error { return nil }(result); err != nil {
		fmt.Fprintln(stdout, summary()+"; report "+path)
		providerError()""", SMOKE),
    ("S7 the text smoke's cap", EVM, "capNano, err := capOf(getenv, DefaultPhotoSmokeCap)", "capNano, err := capOf(getenv, DefaultSmokeCap)", SMOKE),
    ("S8 the peak not kept", EVP, "		m.peak = max(m.peak, m.Committed())\n", "", SMOKE),
    ("S9 billed over reserved not observed", EVP, "if rep.Judge.ReservedInput > 0 && rep.Judge.BilledInput > rep.Judge.ReservedInput {",
     "if rep.Judge.ReservedInput > 0 && rep.Judge.BilledInput < rep.Judge.ReservedInput {", SMOKE),
    ("S10 SAM's images not observed", EVP, "		if c.imagesProcessed != 1 {\n\t\t\trep.Warnings[WarningImagesProcessed]++",
     "		if false {\n\t\t\trep.Warnings[WarningImagesProcessed]++", SMOKE),
    ("S11 the record's kind is the text smoke's", EVM, "if err := rec.Open(runID, PhotoSmokeKind, start, v.Model, approval, capNano); err != nil {",
     "if err := rec.Open(runID, \"smoke\", start, v.Model, approval, capNano); err != nil {", SMOKE),
    ("S12 the report named as the text smoke's", EVL, """	case PhotoSmokeKind:
		prefix = "live-smoke-photo-\"""", """	case PhotoSmokeKind:
		prefix = "live-smoke-\"""", SMOKE),
    ("S13 a failed pipeline passes", EVP, """		rep.Status, rep.Gates[GatePipelineFailed] = "failed", 1""", """		rep.Status = "failed\"""", SMOKE),
    ("S14 the frame not observed", EVP, "			if c.width != rep.Image.Width || c.height != rep.Image.Height {",
     "			if false {", SMOKE),
    ("S15 the inputs not verified as the worker does", EVP, """	if err := photo.Verify(in.stored.Data, in.stored.Record()); err != nil {
		return nil, fmt.Errorf("aievals: the demo image does not verify: %w", err)
	}""", "", SMOKE),
    ("S16 the cap not held below the daily limit", EVM, """	if capNano > ai.Limits.Day {
		return refuse("the run's cap (USD %s) is above the deployment's daily limit (USD %s, within the monthly USD %s)",
			spend.FormatUSD(capNano), spend.FormatUSD(ai.Limits.Day), spend.FormatUSD(ai.Limits.Month))
	}
	reg, err := packs.Registry()
	if err != nil {
		return refuse("the packs cannot be built")
	}
	in, err""", """	reg, err := packs.Registry()
	if err != nil {
		return refuse("the packs cannot be built")
	}
	in, err""", SMOKE),
]


def env(db):
    e = {k: v for k, v in os.environ.items() if k not in AI_VARS}
    e.update({"GOTOOLCHAIN": "local", "PATH": os.path.dirname(GO) + ":" + e.get("PATH", "")})
    if db:
        e.update({"GEMINUS_TEST_DATABASE_URL": DB, "GEMINUS_REQUIRE_DB_TESTS": "1", "DATABASE_URL": DB})
    return e


def run(pkgs, pattern, db):
    cmd = [GO, "test", "-count=1", "-p", "1"]
    if pattern:
        cmd += ["-run", pattern]
    cmd += pkgs
    p = subprocess.run(cmd, cwd=SRC, env=env(db), capture_output=True, text=True, timeout=1800)
    return p.returncode, p.stdout + p.stderr


def main():
    only = sys.argv[1:]
    results = []
    chosen = [m for m in MUTANTS if not only or any(m[0].startswith(o + " ") for o in only)]
    # Every test set must pass on the unmutated tree first: a kill on a
    # tree whose tests already fail means nothing.
    for pkgs, pattern, db in {(tuple(m[4][0]), m[4][1], m[4][2]) for m in chosen}:
        code, out = run(list(pkgs), pattern, db)
        print("BASELINE", " ".join(pkgs), pattern or "(all)", "exit", code, flush=True)
        if code != 0:
            print(out[-3000:])
            sys.exit(3)
    for mid, path, old, new, (pkgs, pattern, db) in chosen:
        full = os.path.join(SRC, path)
        orig = open(full, "rb").read()
        text = orig.decode()
        n = text.count(old)
        if n != 1:
            results.append({"id": mid, "result": "NOT APPLIED", "matches": n})
            print(mid, "NOT APPLIED", n, flush=True)
            continue
        open(full, "w").write(text.replace(old, new))
        try:
            code, out = run(pkgs, pattern, db)
        finally:
            open(full, "wb").write(orig)
        if open(full, "rb").read() != orig:
            print("RESTORE FAILED", mid)
            sys.exit(2)
        if "[build failed]" in out or "[setup failed]" in out or "build failed" in out:
            result = "INVALID"
        elif code != 0:
            result = "KILLED"
        else:
            result = "SURVIVED"
        fails = [l for l in out.splitlines() if l.startswith("--- FAIL") or l.startswith("FAIL") or "panic:" in l][:6]
        results.append({"id": mid, "file": path, "result": result, "exit": code, "failing": fails})
        print(mid, result, fails[:3], flush=True)
    json.dump(results, open(os.path.join(HERE, "mutants.json"), "w"), indent=1)
    k = sum(r["result"] == "KILLED" for r in results)
    print(f"{k}/{len(results)} killed; survived: {[r['id'] for r in results if r['result'] == 'SURVIVED']}; "
          f"invalid or not applied: {[r['id'] for r in results if r['result'] in ('INVALID', 'NOT APPLIED')]}")


if __name__ == "__main__":
    main()
