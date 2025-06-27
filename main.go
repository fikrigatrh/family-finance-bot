package main

import (
	"context"
	"database/sql"
	"financial-bot/models"
	"fmt"
	"github.com/aarondl/null/v8"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/aarondl/sqlboiler/v4/queries/qm"
	_ "github.com/mattn/go-sqlite3"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var (
	db          *sql.DB
	client      *whatsmeow.Client
	currentTime = time.Now
)

const (
	financeDBPath  = "data/app.db"
	whatsappDBPath = "data/whatsapp.db"
)

func main() {

	var err error

	dir, err := os.Getwd()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Current working directory:", dir)

	// Initialize databases
	db, err := initDatabaseNew(financeDBPath, "finance")
	if err != nil {
		log.Fatalf("Finance DB init failed: %v", err)
	}
	defer db.Close()

	// Check if database file exists
	if _, err := os.Stat(financeDBPath); os.IsNotExist(err) {
		// Create new database
		file, err := os.Create(financeDBPath)
		if err != nil {
			log.Fatal("Failed to create database:", err)
		}
		file.Close()

		// Initialize schema
		initDatabase(db)
	}

	whatsappDB, err := initDatabaseNew(whatsappDBPath, "whatsapp")
	if err != nil {
		return
	}
	// Initialize WhatsApp client
	initWhatsAppClient(whatsappDB)

	// Listen to Ctrl-C
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	client.Disconnect()
}

func initDatabase(db *sql.DB) {
	// Create transactions table
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL CHECK(type IN ('income', 'expense')),
		description TEXT NOT NULL,
		amount INTEGER NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		log.Fatal("Failed to create transactions table:", err)
	}
}

func initDatabaseNew(path string, dbType string) (*sql.DB, error) {
	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Open database connection
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys
	db.Exec("PRAGMA foreign_keys = ON;")

	// Run migrations if it's the finance database
	if dbType == "finance" {
		if err := migrateDatabase(db, "app"); err != nil {
			db.Close()
			return nil, fmt.Errorf("database migration failed: %w", err)
		}
	}

	if dbType == "whatsapp" {
		if err := migrateDatabase(db, "whatsapp"); err != nil {
			db.Close()
			return nil, fmt.Errorf("database migration failed: %w", err)
		}
	}

	return db, nil
}

func initWhatsAppClient(waDb *sql.DB) {
	ctx := context.Background()
	// WhatsApp database setup
	container := sqlstore.NewWithDB(waDb, "sqlite3", nil)

	// If you want multiple devices, use container.GetFirstDevice() instead
	deviceStore, _ := container.GetFirstDevice(ctx)
	client = whatsmeow.NewClient(deviceStore, nil)
	client.AddEventHandler(eventHandler)

	// Connect to WhatsApp
	if client.Store.ID == nil {
		// First login - show QR code
		qrChan, _ := client.GetQRChannel(context.Background())
		err := client.Connect()
		if err != nil {
			log.Fatal(err)
		}

		for evt := range qrChan {
			switch evt.Event {
			case "code":
				fmt.Println("Scan QR code:", evt.Code)
			case "success":
				fmt.Println("Logged in successfully!")
			case "timeout":
				log.Fatal("QR scan timed out. Please restart the app.")
			}
		}
	} else {
		// Already logged in
		err := client.Connect()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Connected to WhatsApp")
	}
}

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		log.Printf("Received message from %s in chat %s",
			v.Info.Sender, v.Info.Chat)
		handleMessage(v)
	}
}

func handleMessage(msg *events.Message) {
	// Only handle text messages
	if msg.Message.GetConversation() == "" {
		return
	}

	// Verify sender (only you and your wife)
	sender := msg.Info.Sender.String()
	if !isAuthorizedUser(sender) {
		return
	}

	content := strings.ToLower(strings.TrimSpace(msg.Message.GetConversation()))
	args := strings.Split(content, "\n")

	args[0] = strings.TrimSpace(args[0])
	switch args[0] {
	case "income":
		processTransaction(msg.Info.Chat, "income", args[1:])
	case "expense":
		processTransaction(msg.Info.Chat, "expense", args[1:])
	case "today's mutation":
		getMutations(msg.Info.Chat, currentTime().Format("2006-01-02"))
	case "month's mutation":
		getMonthlyMutations(msg.Info.Chat)
	default:
		if strings.HasPrefix(args[0], "mutation date ") {
			date := strings.TrimPrefix(args[0], "mutation date ")
			getMutations(msg.Info.Chat, date)
		}
	}
}

func isAuthorizedUser(sender string) bool {
	// Replace with your and your wife's phone numbers
	authorizedNumbers := []string{
		"6281337860558@s.whatsapp.net", // Your number
		"6285156184457@s.whatsapp.net", // Wife's number
	}
	for _, num := range authorizedNumbers {
		if sender == num {
			return true
		}
	}
	return false
}

func processTransaction(chat types.JID, txType string, lines []string) {
	totalAmount := 0

	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) < 2 {
			continue
		}

		desc := strings.TrimSpace(parts[0])
		amountStr := regexp.MustCompile(`[^0-9]`).ReplaceAllString(parts[1], "")
		amount, err := strconv.Atoi(amountStr)
		if err != nil {
			continue
		}

		if txType == "expense" {
			amount = -amount
		} else {
			totalAmount += amount
		}

		// Save to database
		tx := &models.Transaction{
			Type:        txType,
			Description: null.StringFrom(desc),
			Amount:      int64(amount),
			CreatedAt:   currentTime(),
		}
		tx.Insert(context.Background(), db, boil.Infer())
	}

	// Calculate and send current balance
	balance := getCurrentBalance()
	response := fmt.Sprintf("💰 *Financial Update* 💰\nDate: %s\nNew Balance: Rp %s",
		currentTime().Format("2006-01-02"),
		formatCurrency(balance))

	sendMessage(chat, response)
}

func getMutations(chat types.JID, date string) {
	start, err := time.Parse("2006-01-02", date)
	if err != nil {
		sendMessage(chat, "⚠️ Invalid date format. Use YYYY-MM-DD")
		return
	}
	end := start.Add(24 * time.Hour)

	transactions, err := models.Transactions(
		qm.Where("created_at >= ? AND created_at < ?", start, end),
		qm.OrderBy("created_at ASC"),
	).All(context.Background(), db)

	if err != nil {
		log.Println("Error fetching transactions:", err)
		sendMessage(chat, "❌ Error fetching transactions")
		return
	}

	response := buildMutationResponse(transactions, date)
	sendMessage(chat, response)
}

func getMonthlyMutations(chat types.JID) {
	now := currentTime()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	monthYear := now.Format("January 2006")

	transactions, err := models.Transactions(
		qm.Where("created_at >= ? AND created_at < ?", start, end),
		qm.OrderBy("created_at ASC"),
	).All(context.Background(), db)

	if err != nil {
		log.Println("Error fetching monthly transactions:", err)
		sendMessage(chat, "❌ Error fetching monthly transactions")
		return
	}

	response := buildMutationResponse(transactions, "Month of "+monthYear)
	sendMessage(chat, response)
}

func getCurrentBalance() int64 {
	var balance int64
	db.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM transactions").Scan(&balance)
	return balance
}

func formatCurrency(amount int64) string {
	str := fmt.Sprintf("%d", amount)
	var parts []string

	// Handle negative numbers
	negative := false
	if amount < 0 {
		negative = true
		str = str[1:]
	}

	// Format with thousand separators
	for len(str) > 3 {
		parts = append([]string{str[len(str)-3:]}, parts...)
		str = str[:len(str)-3]
	}
	if str != "" {
		parts = append([]string{str}, parts...)
	}

	result := strings.Join(parts, ".")
	if negative {
		result = "-" + result
	}
	return result
}

func buildMutationResponse(transactions []*models.Transaction, period string) string {
	if len(transactions) == 0 {
		return fmt.Sprintf("📊 *Transaction Report*\nPeriod: %s\n\nNo transactions found", period)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 *Transaction Report*\nPeriod: %s\n\n", period))

	var total int64
	for _, tx := range transactions {
		total += tx.Amount
		sign := ""
		if tx.Amount > 0 {
			sign = "+"
		}
		sb.WriteString(fmt.Sprintf("⏰ %s\n%s: %sRp %s\n\n",
			tx.CreatedAt.Format("Mon, 02 Jan 2006 15:04"),
			tx.Description.String,
			sign,
			formatCurrency(tx.Amount)))
	}

	sb.WriteString(fmt.Sprintf("💵 *Total Balance*: Rp %s", formatCurrency(total)))
	return sb.String()
}

func sendMessage(chat types.JID, text string) {
	_, err := client.SendMessage(context.Background(), chat, &waProto.Message{
		Conversation: proto.String(text),
	})
	if err != nil {
		log.Println("Error sending message:", err)
	}
}
