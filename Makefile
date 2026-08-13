.PHONY: test bench bench-check bench-baseline fuzz-smoke race check vet

FUZZTIME ?= 30s

test:
	go test ./...

vet:
	gofmt -l . ; go vet ./...

# One process, shuffled, minimum of the count -- the numbers the README quotes.
bench:
	go test -run '^$$' -bench . -benchmem -count=6 -shuffle=on .

# The previous target piped through tee with no pipefail and then ended in an
# unconditional echo, so the status make recorded was the echo's: it could not
# fail. No pipe carries the verdict now, and the comparison is real.
bench-check:
	./scripts/bench-check.sh

# Regenerate the baseline on a quiet machine (load average under 1): the floor
# compares wall-clock, and a baseline captured under load is a slow baseline
# that hides regressions. The header records the load it was taken at.
bench-baseline:
	@echo "# captured $$(date -u +%Y-%m-%dT%H:%M:%SZ) load:$$(cut -d' ' -f1 /proc/loadavg) $$(go version)" > testdata/bench.txt
	go test -run '^$$' -bench . -count=6 -shuffle=on . >> testdata/bench.txt

fuzz-smoke:
	go test -run '^$$' -fuzz FuzzUnmarshalNeverPanics -fuzztime $(FUZZTIME) .

race:
	go test -race ./...

check: vet test race fuzz-smoke
	@echo "check: green"
