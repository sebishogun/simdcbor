package codec

import "testing"

// The filter shapes, side by side on one stream of 2,000 records with a 1%
// match rate: decode everything, skip and decode the matches, or frame
// everything and decode the matches from the frames.
func BenchmarkFilter(b *testing.B) {
	stream := recordStream(b, 2000)
	lim := Limits{}

	b.Run("decode-all", func(b *testing.B) {
		b.SetBytes(int64(len(stream)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d := New(stream, lim)
			for d.More() {
				if _, err := d.Decode(); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("skip-then-decode-1pct", func(b *testing.B) {
		b.SetBytes(int64(len(stream)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d := New(stream, lim)
			rec := 0
			for d.More() {
				if rec%100 == 0 {
					if _, err := d.Decode(); err != nil {
						b.Fatal(err)
					}
				} else if _, err := d.Skip(); err != nil {
					b.Fatal(err)
				}
				rec++
			}
		}
	})

	b.Run("frame-then-decode-1pct", func(b *testing.B) {
		b.SetBytes(int64(len(stream)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d := New(stream, lim)
			rec := 0
			for d.More() {
				r, err := d.RawNext()
				if err != nil {
					b.Fatal(err)
				}
				if rec%100 == 0 {
					if _, err := r.Decode(lim); err != nil {
						b.Fatal(err)
					}
				}
				rec++
			}
		}
	})

	// Framing alone, with nothing decoded: the floor a filter cannot go below.
	b.Run("frame-only", func(b *testing.B) {
		b.SetBytes(int64(len(stream)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			d := New(stream, lim)
			for d.More() {
				if _, err := d.RawNext(); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
