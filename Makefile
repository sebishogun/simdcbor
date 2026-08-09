.PHONY: test bench bench-check vet

test:
	go test ./...

vet:
	gofmt -l . ; go vet ./...

# One process, shuffled, minimum of the count -- the numbers the README quotes.
bench:
	go test -run '^$$' -bench . -benchmem -count=6 -shuffle=on .

bench-check:
	@go test -run '^$$' -bench . -count=6 -shuffle=on . | tee /tmp/simdcbor-bench.txt
	@echo "compare /tmp/simdcbor-bench.txt against testdata/bench.txt (8% floor)"
