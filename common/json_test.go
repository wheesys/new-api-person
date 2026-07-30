package common

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJsonRawMessageToString(t *testing.T) {
	tests := []struct {
		name string
		data json.RawMessage
		want string
	}{
		{
			name: "object",
			data: json.RawMessage(`{"city":"Paris","days":0,"strict":false}`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "string",
			data: json.RawMessage(`"{\"city\":\"Paris\",\"days\":0,\"strict\":false}"`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "null",
			data: json.RawMessage(`null`),
			want: "",
		},
		{
			name: "empty",
			data: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, JsonRawMessageToString(tt.data))
		})
	}
}

func TestValidateJsonUniqueObjectKeys(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr error
	}{
		{name: "unique nested objects", data: `{"result":{"status":"ok"},"items":[{"id":1},{"id":2}]}`},
		{name: "duplicate root key", data: `{"status":"failed","status":"ok"}`, wantErr: ErrJsonDuplicateObjectKey},
		{name: "duplicate nested key", data: `{"result":{"status":"failed","status":"ok"}}`, wantErr: ErrJsonDuplicateObjectKey},
		{name: "invalid json", data: `{"status":`, wantErr: ErrJsonInvalidStructure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateJsonUniqueObjectKeys([]byte(test.data))
			if test.wantErr == nil {
				require.NoError(t, err)
				return
			}
			if errors.Is(test.wantErr, ErrJsonDuplicateObjectKey) {
				require.ErrorIs(t, err, ErrJsonDuplicateObjectKey)
				return
			}
			require.Error(t, err)
		})
	}
}
