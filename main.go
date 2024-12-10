// main.go
// main.go
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// Config holds database configuration
type Config struct {
	CouchDB struct {
		URL    string `yaml:"url"`
		Bucket string `yaml:"bucket"`
		User   string `yaml:"user"`
		Pwd    string `yaml:"pwd"`
	} `yaml:"couchdb"`
}

// Request models
type VolumeRequest struct {
	Data struct {
		FrameID string   `json:"frame_id"`
		Volumes []Volume `json:"volumes"`
	} `json:"data"`
}

type Volume struct {
	ObjectName      string  `json:"object_name"`
	UncertaintyCups float64 `json:"uncertainty_cups"`
	VolumeCups      float64 `json:"volume_cups"`
}

// Response models
type MacroResponse struct {
	Data []MacroData `json:"data"`
}

type MacroData struct {
	Found            bool    `json:"found"`
	Macros           Macros  `json:"macros"`
	RequestedFood    string  `json:"requested_food"`
	RequestedVolume  float64 `json:"requested_volume"`
	CalculatedWeight float64 `json:"calculated_weight"`
}

type Macros struct {
	Calories float64 `json:"calories"`
	Carbs    float64 `json:"carbs"`
	Fat      float64 `json:"fat"`
	Protein  float64 `json:"protein"`
}

// Food data models
type FoodData struct {
	Description   string     `json:"description"`
	FdcID         int        `json:"fdcId"` // Changed from string to int
	FoodNutrients []Nutrient `json:"foodNutrients"`
	FoodPortions  []Portion  `json:"foodPortions"`
}

type Nutrient struct {
	Amount   float64 `json:"amount"`
	Nutrient struct {
		Name   string `json:"name"`
		Number string `json:"number"`
	} `json:"nutrient"`
}

type Portion struct {
	GramWeight  float64 `json:"gramWeight"`
	ID          int     `json:"id"`
	MeasureUnit struct {
		Abbreviation string `json:"abbreviation"`
		ID           int    `json:"id"`
		Name         string `json:"name"`
	} `json:"measureUnit"`
	Modifier           string `json:"modifier"`
	PortionDescription string `json:"portionDescription"`
	SequenceNumber     int    `json:"sequenceNumber"`
}

// Database represents our CouchDB connection
type Database struct {
	cluster    *gocb.Cluster
	bucket     *gocb.Bucket
	scope      *gocb.Scope
	collection *gocb.Collection
}

var db *Database

func loadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %v", err)
	}

	// Override with environment variables if they exist
	if url := os.Getenv("COUCHDB_URL"); url != "" {
		config.CouchDB.URL = url
	}
	if bucket := os.Getenv("COUCHDB_BUCKET"); bucket != "" {
		config.CouchDB.Bucket = bucket
	}
	if user := os.Getenv("COUCHDB_USER"); user != "" {
		config.CouchDB.User = user
	}
	if pwd := os.Getenv("COUCHDB_PWD"); pwd != "" {
		config.CouchDB.Pwd = pwd
	}

	return &config, nil
}

func main() {
	// Initialize database connection
	var err error
	db, err = initDB()
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	router := gin.Default()
	router.POST("/v1/calculate-macros", calculateMacros)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Starting server on port %s", port)
	router.Run(":" + port)
}

func initDB() (*Database, error) {
	config, err := loadConfig("config.yaml")
	if err != nil {
		log.Printf("Warning: Failed to load config file: %v", err)
		config = &Config{}
		config.CouchDB.URL = os.Getenv("COUCHBASE_URL")
		config.CouchDB.Bucket = os.Getenv("COUCHBASE_BUCKET")
		config.CouchDB.User = os.Getenv("COUCHBASE_USER")
		config.CouchDB.Pwd = os.Getenv("COUCHBASE_PWD")
	}

	log.Printf("Attempting to connect to Couchbase with URL: %s, Bucket: %s", config.CouchDB.URL, config.CouchDB.Bucket)

	// Configure cluster options for cloud connectivity
	clusterOpts := gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{
			Username: config.CouchDB.User,
			Password: config.CouchDB.Pwd,
		},
		SecurityConfig: gocb.SecurityConfig{
			TLSSkipVerify: false,
		},
		TimeoutsConfig: gocb.TimeoutsConfig{
			ConnectTimeout: time.Second * 30,
			KVTimeout:      time.Second * 30,
			QueryTimeout:   time.Second * 30,
		},
	}

	// Connect to cluster
	cluster, err := gocb.Connect(fmt.Sprintf("couchbases://%s", config.CouchDB.URL), clusterOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cluster: %v", err)
	}

	log.Printf("Successfully connected to cluster, attempting to get bucket: %s", config.CouchDB.Bucket)

	// Try a simple query to verify connectivity
	result, err := cluster.Query(
		"SELECT RAW 1",
		&gocb.QueryOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to execute test query: %v", err)
	}
	result.Close()

	// Get bucket with longer timeout
	bucket := cluster.Bucket(config.CouchDB.Bucket)

	// Increase the wait time for bucket readiness
	err = bucket.WaitUntilReady(30*time.Second, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to bucket: %v", err)
	}

	collection := bucket.DefaultCollection()

	database := &Database{
		cluster:    cluster,
		bucket:     bucket,
		collection: collection,
	}

	log.Printf("Successfully connected to Couchbase and bucket '%s'", config.CouchDB.Bucket)
	return database, nil
}

func calculateMacros(c *gin.Context) {
	var request VolumeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response := MacroResponse{
		Data: make([]MacroData, 0),
	}

	for _, volume := range request.Data.Volumes {
		macroData := processFoodVolume(volume)
		response.Data = append(response.Data, macroData)
	}

	c.JSON(http.StatusOK, response)
}

// Update the struct to match exactly what's in Couchbase
func processFoodVolume(volume Volume) MacroData {
	// Get food data based on object name
	foodData, err := getFoodData(volume.ObjectName)
	if err != nil || foodData == nil {
		log.Printf("Error getting food data: %v", err)
		return MacroData{
			Found:           false,
			RequestedFood:   volume.ObjectName,
			RequestedVolume: volume.VolumeCups,
		}
	}

	// Debug log to see what portions we have
	log.Printf("Available portions for %s:", volume.ObjectName)
	for _, p := range foodData.FoodPortions {
		log.Printf("- Description: %s, Weight: %f", p.PortionDescription, p.GramWeight)
	}

	// Find cup portion measurement with exact matching
	var cupGrams float64
	for _, portion := range foodData.FoodPortions {
		if strings.Contains(portion.PortionDescription, "1 cup") {
			log.Printf("Found cup measurement: %s = %fg", portion.PortionDescription, portion.GramWeight)
			cupGrams = portion.GramWeight
			break
		}
	}

	if cupGrams == 0 {
		log.Printf("No cup measurement found for %s", volume.ObjectName)
		// For eggs specifically, we might need to convert from individual egg weight
		if volume.ObjectName == "egg" {
			// Find "1 egg" portion
			for _, portion := range foodData.FoodPortions {
				if portion.PortionDescription == "1 egg" {
					// Approximate 1 cup as 4-5 large eggs
					cupGrams = portion.GramWeight * 4.5
					break
				}
			}
		}
		if cupGrams == 0 {
			return MacroData{
				Found:           false,
				RequestedFood:   volume.ObjectName,
				RequestedVolume: volume.VolumeCups,
			}
		}
	}

	// Calculate total grams based on requested cups
	calculatedGrams := volume.VolumeCups * cupGrams

	// Get nutrient values
	macros := calculateMacrosForGrams(foodData.FoodNutrients, calculatedGrams, cupGrams)

	return MacroData{
		Found:            true,
		Macros:           macros,
		RequestedFood:    volume.ObjectName,
		RequestedVolume:  volume.VolumeCups,
		CalculatedWeight: calculatedGrams,
	}
}

func calculateMacrosForGrams(nutrients []Nutrient, calculatedGrams, baseGrams float64) Macros {
	var macros Macros
	ratio := calculatedGrams / 100.0 // nutrients are per 100g

	for _, nutrient := range nutrients {
		switch nutrient.Nutrient.Number {
		case "208": // Energy (kcal)
			macros.Calories = nutrient.Amount * ratio
		case "203": // Protein
			macros.Protein = nutrient.Amount * ratio
		case "204": // Total fat
			macros.Fat = nutrient.Amount * ratio
		case "205": // Carbohydrates
			macros.Carbs = nutrient.Amount * ratio
		}
	}

	return macros
}

func getFoodData(objectName string) (*FoodData, error) {
	var searchTerm string
	switch objectName {
	case "egg":
		searchTerm = "Egg, whole, boiled or poached"
	case "rice":
		searchTerm = "Rice, cooked, NFS"
	case "banana":
		searchTerm = "Banana, raw"
	default:
		return nil, fmt.Errorf("unknown food: %s", objectName)
	}

	// Create N1QL query with raw result inspection
	query := "SELECT RAW r FROM fndds r WHERE LOWER(r.description) = LOWER($1) LIMIT 1"

	log.Printf("Executing query: %s with params: [%s]", query, searchTerm)

	// Execute the query
	result, err := db.cluster.Query(
		query,
		&gocb.QueryOptions{
			PositionalParameters: []interface{}{
				searchTerm,
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("query failed: %v", err)
	}
	defer result.Close()

	// Print each raw result for debugging
	var rawResult interface{}
	for result.Next() {
		err = result.Row(&rawResult)
		if err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		// Print the raw result to see what we're getting
		// log.Printf("Raw result: %+v", rawResult)
	}

	// Reset the query for actual processing
	result, err = db.cluster.Query(
		query,
		&gocb.QueryOptions{
			PositionalParameters: []interface{}{
				searchTerm,
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("query failed: %v", err)
	}
	defer result.Close()

	var food FoodData
	if result.Next() {
		err := result.Row(&food)
		if err != nil {
			return nil, fmt.Errorf("failed to decode food data: %v", err)
		}
		log.Printf("Found food: %s with %d portions", food.Description, len(food.FoodPortions))
		return &food, nil
	}

	return nil, fmt.Errorf("no matching food found for: %s", searchTerm)
}
