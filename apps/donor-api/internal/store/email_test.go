package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/store/queries"
)

const emailTestKey = "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="

type recordingMail struct {
	mu       sync.Mutex
	messages []mail.Message
	failure  error
	check    func(mail.Message)
}

func (r *recordingMail) Send(_ context.Context, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.check != nil {
		r.check(m)
	}
	r.messages = append(r.messages, m)
	return r.failure
}
func emailFixture(t *testing.T) (LoginEmailWorker, pgtype.UUID, *recordingMail, time.Time) {
	t.Helper()
	pool := database(t)
	id, email := donor(t, pool)
	accountCleanup(t, pool, id)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM donor_login_emails WHERE donor_id=$1", id); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = QueueLoginEmailInTx(context.Background(), tx, email, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	transport := &recordingMail{}
	return LoginEmailWorker{DB: pool, Settings: mail.Settings{Host: "smtp.fixture", Port: 587, Sender: "sender@example.org", EncryptionKey: emailTestKey}, Origin: "https://donor.example.org", Transport: transport}, id, transport, now
}
func tokenFromMail(t *testing.T, m mail.Message) string {
	t.Helper()
	_, tail, ok := strings.Cut(m.Body, "#token=")
	if !ok {
		t.Fatal("missing claim token")
	}
	token, _, _ := strings.Cut(tail, "\n")
	return token
}
func TestLoginEmailCommitsProofBeforeSendAndReusesItOnRetry(t *testing.T) {
	w, id, transport, now := emailFixture(t)
	ctx := context.Background()
	transport.failure = errors.New("lost acknowledgement with private server content")
	transport.check = func(m mail.Message) {
		token := tokenFromMail(t, m)
		link, err := queries.New(w.DB).LoginLink(ctx, HashToken(token))
		if err != nil || link.DonorID != id {
			t.Fatal("uncommitted credential sent", err)
		}
	}
	if worked, err := w.DeliverNext(ctx, now); err != nil || !worked {
		t.Fatal(worked, err)
	}
	var encrypted []byte
	var attempts int
	var last string
	if err := w.DB.QueryRow(ctx, "SELECT encrypted_token,attempts,last_error FROM donor_login_emails WHERE donor_id=$1", id).Scan(&encrypted, &attempts, &last); err != nil {
		t.Fatal(err)
	}
	token := tokenFromMail(t, transport.messages[0])
	clear, err := credentials.Decrypt(encrypted, emailTestKey)
	if err != nil || string(clear) != token || strings.Contains(string(encrypted), token) || attempts != 1 || last != "DeliveryError" {
		t.Fatal("unsafe retry state", err)
	}
	if worked, err := w.DeliverNext(ctx, now.Add(30*time.Second)); err != nil || worked {
		t.Fatal("retry fired before availability", worked, err)
	}
	transport.failure = nil
	if worked, err := w.DeliverNext(ctx, now.Add(time.Minute)); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if len(transport.messages) != 2 || transport.messages[0] != transport.messages[1] {
		t.Fatal("retry changed link or message identity")
	}
	var complete bool
	if err := w.DB.QueryRow(ctx, "SELECT completed_at IS NOT NULL,encrypted_token FROM donor_login_emails WHERE donor_id=$1", id).Scan(&complete, &encrypted); err != nil || !complete || encrypted != nil {
		t.Fatal("completed job retained credential", err)
	}
	session, err := (AccountService{DB: w.DB}).ClaimAccount(ctx, token, "")
	if err != nil || session == "" {
		t.Fatal("delivered proof failed claim", err)
	}
	if _, err = (AccountService{DB: w.DB}).ClaimAccount(ctx, token, ""); !errors.Is(err, ErrDenied) {
		t.Fatal("proof replay allowed", err)
	}
}
func TestLoginEmailConcurrentWorkersSendOnce(t *testing.T) {
	w, _, transport, now := emailFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; _, err := w.DeliverNext(ctx, now); done <- err }()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if len(transport.messages) != 1 {
		t.Fatalf("deliveries=%d", len(transport.messages))
	}
}
func TestLoginEmailSuppressesDisabledExpiredAndParksFailures(t *testing.T) {
	for _, mode := range []string{"disabled", "expired", "five failures", "bad key"} {
		t.Run(mode, func(t *testing.T) {
			w, id, transport, now := emailFixture(t)
			ctx := context.Background()
			switch mode {
			case "disabled":
				if _, err := w.DB.Exec(ctx, "UPDATE donors SET disabled=true WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			case "expired":
				if _, _, err := w.prepare(ctx, now.Add(-16*time.Minute)); err != nil {
					t.Fatal(err)
				}
				// Preparation selects only available work, so explicitly prepare at queue time
				// and advance beyond the original lifetime without changing its token.
				if _, _, err := w.prepare(ctx, now); err != nil {
					t.Fatal(err)
				}
				now = now.Add(16 * time.Minute)
			case "five failures":
				transport.failure = errors.New("SMTP failed")
			case "bad key":
				w.Settings.EncryptionKey = "not a key"
			}
			if mode == "bad key" {
				if _, err := w.DeliverNext(ctx, now); err == nil {
					t.Fatal("bad key accepted")
				}
				return
			}
			count := 1
			if mode == "five failures" {
				count = 5
			}
			for i := 0; i < count; i++ {
				if worked, err := w.DeliverNext(ctx, now.Add(time.Duration(i)*time.Minute)); err != nil || !worked {
					t.Fatal(worked, err)
				}
			}
			if worked, err := w.DeliverNext(ctx, now.Add(10*time.Minute)); err != nil || worked {
				t.Fatal("terminal job retried", worked, err)
			}
			expected := 0
			if mode == "five failures" {
				expected = 5
			}
			if len(transport.messages) != expected {
				t.Fatal("unexpected delivery", len(transport.messages))
			}
		})
	}
}
