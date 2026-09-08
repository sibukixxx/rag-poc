package usecase

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/demoaccess"
)

const DefaultDemoSessionDuration = 12 * time.Hour

var demoUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,63}$`)

type DemoAccessUseCase struct {
	store           demoaccess.Store
	sessionDuration time.Duration
	now             func() time.Time
}

func NewDemoAccessUseCase(store demoaccess.Store, sessionDuration time.Duration) *DemoAccessUseCase {
	if sessionDuration <= 0 {
		sessionDuration = DefaultDemoSessionDuration
	}
	return &DemoAccessUseCase{store: store, sessionDuration: sessionDuration, now: time.Now}
}

func (u *DemoAccessUseCase) CreateUser(ctx context.Context, username, email, company string, expiresAt time.Time) (demoaccess.User, string, error) {
	username = strings.TrimSpace(username)
	email = strings.ToLower(strings.TrimSpace(email))
	company = strings.TrimSpace(company)
	if !demoUsernamePattern.MatchString(username) {
		return demoaccess.User{}, "", errors.New("username must be 3-64 characters using letters, numbers, dot, underscore, or hyphen")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) {
		return demoaccess.User{}, "", errors.New("a valid email address is required")
	}
	if company == "" || len([]rune(company)) > 120 {
		return demoaccess.User{}, "", errors.New("company is required and must be at most 120 characters")
	}
	if !expiresAt.After(u.now()) {
		return demoaccess.User{}, "", errors.New("expiry must be in the future")
	}
	password, err := generateDemoPassword(20)
	if err != nil {
		return demoaccess.User{}, "", err
	}
	hash, err := hashDemoPassword(password)
	if err != nil {
		return demoaccess.User{}, "", fmt.Errorf("hashing password: %w", err)
	}
	user := demoaccess.User{
		ID: uuid.NewString(), Username: username, Email: email, Company: company,
		PasswordHash: hash, ExpiresAt: expiresAt.UTC(), CreatedAt: u.now().UTC(),
	}
	if err := u.store.CreateUser(ctx, user); err != nil {
		return demoaccess.User{}, "", err
	}
	return user, password, nil
}

func (u *DemoAccessUseCase) Authenticate(ctx context.Context, username, password, accessEmail string) (demoaccess.User, string, time.Time, error) {
	user, err := u.store.GetUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return demoaccess.User{}, "", time.Time{}, demoaccess.ErrInvalidCredentials
	}
	now := u.now()
	if !user.Available(now) {
		return demoaccess.User{}, "", time.Time{}, demoaccess.ErrAccountUnavailable
	}
	if !verifyDemoPassword(user.PasswordHash, password) {
		return demoaccess.User{}, "", time.Time{}, demoaccess.ErrInvalidCredentials
	}
	if accessEmail != "" && !strings.EqualFold(strings.TrimSpace(accessEmail), user.Email) {
		return demoaccess.User{}, "", time.Time{}, demoaccess.ErrInvalidCredentials
	}
	token, tokenHash, err := generateSessionToken()
	if err != nil {
		return demoaccess.User{}, "", time.Time{}, err
	}
	expiresAt := now.Add(u.sessionDuration)
	if user.ExpiresAt.Before(expiresAt) {
		expiresAt = user.ExpiresAt
	}
	if err := u.store.CreateSession(ctx, tokenHash, user.ID, expiresAt); err != nil {
		return demoaccess.User{}, "", time.Time{}, err
	}
	return user, token, expiresAt, nil
}

func (u *DemoAccessUseCase) CurrentUser(ctx context.Context, token, accessEmail string) (demoaccess.User, error) {
	if token == "" {
		return demoaccess.User{}, demoaccess.ErrInvalidCredentials
	}
	user, err := u.store.GetUserBySession(ctx, hashDemoToken(token), u.now())
	if err != nil || !user.Available(u.now()) {
		return demoaccess.User{}, demoaccess.ErrInvalidCredentials
	}
	if accessEmail != "" && !strings.EqualFold(strings.TrimSpace(accessEmail), user.Email) {
		return demoaccess.User{}, demoaccess.ErrInvalidCredentials
	}
	return user, nil
}

func (u *DemoAccessUseCase) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return u.store.DeleteSession(ctx, hashDemoToken(token))
}

func generateSessionToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, hashDemoToken(token), nil
}

func hashDemoToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func generateDemoPassword(length int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, length)
	random := make([]byte, length)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generating password: %w", err)
	}
	for i := range b {
		b[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(b), nil
}

const demoPasswordIterations = 600_000

func hashDemoPassword(password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating password salt: %w", err)
	}
	derived := pbkdf2SHA256([]byte(password), salt, demoPasswordIterations, 32)
	encoded := fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", demoPasswordIterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(derived))
	return []byte(encoded), nil
}

func verifyDemoPassword(encoded []byte, password string) bool {
	parts := strings.Split(string(encoded), "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 100_000 || iterations > 2_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != 32 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return hmac.Equal(got, want)
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLength int) []byte {
	hashLength := sha256.Size
	blocks := (keyLength + hashLength - 1) / hashLength
	derived := make([]byte, 0, blocks*hashLength)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		derived = append(derived, t...)
	}
	return derived[:keyLength]
}
