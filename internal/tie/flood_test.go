package tie

import (
	"testing"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

func makeHeaders(n int) []encoding.TIEHeaderWithLifeTime {
	headers := make([]encoding.TIEHeaderWithLifeTime, n)
	for i := range headers {
		headers[i] = encoding.TIEHeaderWithLifeTime{
			Header: encoding.TIEHeader{
				TIEID: encoding.TIEID{
					Direction:  encoding.TieDirectionSouth,
					Originator: encoding.SystemIDType(i + 1),
					TIEType:    encoding.TIETypeNodeTIEType,
					TIENr:      1,
				},
				SeqNr: 1,
			},
			RemainingLifetime: encoding.DefaultLifetime,
		}
	}
	return headers
}

func TestBuildTIDEChunks(t *testing.T) {
	tests := []struct {
		name       string
		nHeaders   int
		wantChunks int
	}{
		{"empty", 0, 1},
		{"one header", 1, 1},
		{"max minus one", MaxTIDEHeaders - 1, 1},
		{"exactly max", MaxTIDEHeaders, 1},
		{"max plus one", MaxTIDEHeaders + 1, 2},
		{"two times max", 2 * MaxTIDEHeaders, 2},
		{"two times max plus one", 2*MaxTIDEHeaders + 1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := makeHeaders(tt.nHeaders)
			chunks := buildTIDEChunks(headers)

			if len(chunks) != tt.wantChunks {
				t.Fatalf("got %d chunks, want %d", len(chunks), tt.wantChunks)
			}

			// First chunk must start at MinTIEID.
			if chunks[0].StartRange != MinTIEID {
				t.Errorf("first chunk StartRange = %v, want MinTIEID", chunks[0].StartRange)
			}

			// Last chunk must end at MaxTIEID.
			if chunks[len(chunks)-1].EndRange != MaxTIEID {
				t.Errorf("last chunk EndRange = %v, want MaxTIEID", chunks[len(chunks)-1].EndRange)
			}

			// No empty chunks (except the empty-LSDB case).
			if tt.nHeaders > 0 {
				for i, c := range chunks {
					if len(c.Headers) == 0 {
						t.Errorf("chunk %d has 0 headers", i)
					}
				}
			}

			// Contiguous tiling: chunk[i].EndRange == chunk[i+1].StartRange.
			for i := 0; i < len(chunks)-1; i++ {
				if chunks[i].EndRange != chunks[i+1].StartRange {
					t.Errorf("gap between chunk %d EndRange=%v and chunk %d StartRange=%v",
						i, chunks[i].EndRange, i+1, chunks[i+1].StartRange)
				}
			}

			// Total headers across all chunks equals input.
			total := 0
			for _, c := range chunks {
				total += len(c.Headers)
			}
			if total != tt.nHeaders {
				t.Errorf("total headers = %d, want %d", total, tt.nHeaders)
			}
		})
	}
}
