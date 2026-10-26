package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	"image/png"
	"strings"
	"time"

	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
)

func (s PartnerDashboardService) SaveLogo(ctx context.Context, token, dataURL string) error {
	var clean []byte
	if dataURL != "" {
		if len(dataURL) > 350000 {
			return ErrPartnerApplication
		}
		prefix, encoded, ok := strings.Cut(dataURL, ",")
		if !ok || (prefix != "data:image/png;base64" && prefix != "data:image/jpeg;base64") {
			return ErrPartnerApplication
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(raw) > 256<<10 {
			return ErrPartnerApplication
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 512 ||
			config.Height > 512 {
			return ErrPartnerApplication
		}
		decoded, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return ErrPartnerApplication
		}
		var out bytes.Buffer
		if err := png.Encode(&out, decoded); err != nil {
			return err
		}
		if out.Len() > 1<<20 {
			return ErrPartnerApplication
		}
		clean = out.Bytes()
	}
	_, err := partnerAdminWrite(ctx, s.DB, token, func(tx pgx.Tx, actor PartnerActor, _ time.Time) (struct{}, error) {
		return struct{}{}, queries.New(tx).SetPartnerLogo(ctx, queries.SetPartnerLogoParams{ID: actor.PartnerID, LogoPng: clean})
	})
	return err
}

func (s PartnerDashboardService) Logo(ctx context.Context, token string) ([]byte, error) {
	return partnerRead(ctx, s.DB, token, func(q *queries.Queries, actor PartnerActor) ([]byte, error) {
		return q.PartnerLogo(ctx, actor.PartnerID)
	})
}
