package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDB struct {
	Message *mongo.Collection
}

func NewDatabase(Message *mongo.Collection) *MongoDB {
	return &MongoDB{
		Message: Message,
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (m *MongoDB) GetByRoomId(w http.ResponseWriter, r *http.Request)

func (m *MongoDB) handleConnections(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	name := params.Get("room")
	if name == "" {
		http.Error(w, "Missing room parameter", http.StatusBadRequest)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer ws.Close()

	// if _, ok := clients[name]; !ok {
	// 	clients[name] = make(map[*websocket.Conn]bool)
	// }

	if _, ok := clients[name]; !ok {
		clients[name][ws] = true
	}

	// clients[name][ws] = true

	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Println(err)
			delete(clients[name], ws)
			break
		}

		log.Printf("Received message: %+v\n", msg)

		if msg.Image != "" {
			filePath, err := saveImageToFolder(msg.Image)
			if err != nil {
				log.Println("Error saving image:", err)
				continue
			}
			imageURL := os.Getenv("API_URL")
			msg.Image = imageURL + "/images/" + filepath.Base(filePath)
		}

		msg.Room = name

		_, err = m.Message.InsertOne(context.TODO(), msg)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func saveImageToFolder(base64Image string) (string, error) {
	const dataURLPrefix = "data:image/"
	if strings.HasPrefix(base64Image, dataURLPrefix) {
		splitData := strings.SplitN(base64Image, ",", 2)
		if len(splitData) != 2 {
			return "", fmt.Errorf("invalid data URL format")
		}
		base64Image = splitData[1]
	}

	imgBytes, err := base64.StdEncoding.DecodeString(base64Image)
	if err != nil {
		return "", fmt.Errorf("error decoding base64 string: %w", err)
	}

	filename := fmt.Sprintf("image_%d.jpg", time.Now().UnixNano())
	filePath := filepath.Join("images", filename)

	err = os.MkdirAll("images", os.ModePerm)
	if err != nil {
		return "", fmt.Errorf("error creating directory: %w", err)
	}

	err = os.WriteFile(filePath, imgBytes, 0644)
	if err != nil {
		return "", fmt.Errorf("error writing file: %w", err)
	}

	log.Printf("Image saved to %s", filePath)

	return filePath, nil
}

type Message struct {
	Username string `json:"username" bson:"username"`
	Message  string `json:"message" bson:"message"`
	Image    string `json:"image" bson:"image"` // This will be the file path (URL) in the response
	Room     string `json:"room" bson:"room"`
}

type FullDocument struct {
	Username string `json:"username" bson:"username"`
	Message  string `json:"message" bson:"message"`
	Image    string `json:"image" bson:"image"` // This will be the file path (URL) in the response
	Room     string `json:"room" bson:"room"`
}

type ChangeEvent struct {
	OperationType string       `bson:"operationType"`
	FullDocument  FullDocument `bson:"fullDocument"`
	DocumentKey   struct {
		ID string `bson:"_id"`
	} `bson:"documentKey"`
	NS struct {
		Coll string `bson:"coll"`
		DB   string `bson:"db"`
	} `bson:"ns"`
}

var clients = make(map[string]map[*websocket.Conn]bool)

func (m *MongoDB) handleMessages() {
	pipeline := mongo.Pipeline{}
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	changeStream, err := m.Message.Watch(context.TODO(), pipeline, opts)
	if err != nil {
		log.Fatal(err)
	}
	defer changeStream.Close(context.TODO())

	for changeStream.Next(context.TODO()) {
		var changeEvent ChangeEvent
		if err := changeStream.Decode(&changeEvent); err != nil {
			log.Fatal(err)
		}

		room := changeEvent.FullDocument.Room
		for client := range clients[room] {
			err := client.WriteJSON(changeEvent.FullDocument)
			if err != nil {
				log.Println(err)
				client.Close()
				delete(clients[room], client)
			}
		}
		fmt.Printf("Change detected: %+v\n", changeEvent.FullDocument)
	}
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file")
	}
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(context.TODO())

	collection := client.Database("aronachat").Collection("simp")

	monggose := NewDatabase(collection)

	fs := http.FileServer(http.Dir("./public"))
	http.Handle("/", fs)
	http.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir("./images"))))
	http.HandleFunc("/ws", monggose.handleConnections)
	http.HandleFunc("/get-chat/{room-id}", monggose.GetByRoomId)

	go monggose.handleMessages()

	appHost := os.Getenv("APP_HOST")
	if appHost == "" {
		appHost = ":8000"
	}

	fmt.Println("Server started on", appHost)
	err = http.ListenAndServe(appHost, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
