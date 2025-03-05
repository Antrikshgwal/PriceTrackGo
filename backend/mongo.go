package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Price struct {
	Value     float64   `bson:"value"`
	Timestamp time.Time `bson:"timestamp"`
}

type Product struct {
	ProductURL     string   `bson:"product_url"`
	ProductName    string   `bson:"product_name"`
	ImageURL       string   `bson:"image_url"`
	Specifications []string `bson:"specifications"`
	PriceHistory   []Price  `bson:"price_history"`
}

type MongoDB struct {
	client     *mongo.Client
	database   *mongo.Database
	collection *mongo.Collection
}

func NewMongoDB(uri string, username string) (*MongoDB, error) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}

	// Ping the database to verify connection
	err = client.Ping(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	database := client.Database("price_tracker")
	collection := database.Collection(username)

	// Create indexes
	_, err = collection.Indexes().CreateOne(
		context.Background(),
		mongo.IndexModel{
			Keys: bson.D{
				{Key: "product_url", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
	)
	if err != nil {
		return nil, err
	}

	return &MongoDB{
		client:     client,
		database:   database,
		collection: collection,
	}, nil
}

func (m *MongoDB) UpsertProduct(product *Product) error {
	// 1. Check for existing product
	var existingProduct Product
	err := m.collection.FindOne(
		context.Background(),
		bson.M{"product_url": product.ProductURL},
	).Decode(&existingProduct)

	// 2. Handle the error check
	if err != nil && err != mongo.ErrNoDocuments {
		return fmt.Errorf("error checking existing product: %v", err)
	}

	// 3. Preserve price history if product exists
	if err != mongo.ErrNoDocuments {
		product.PriceHistory = existingProduct.PriceHistory
	}

	// 4. Perform the upsert operation
	filter := bson.M{"product_url": product.ProductURL}
	update := bson.M{"$set": product}
	opts := options.Update().SetUpsert(true)

	_, err = m.collection.UpdateOne(context.Background(), filter, update, opts)
	return err
}

func (m *MongoDB) AddPrice(productURL string, price float64) error {
	newPrice := Price{
		Value:     price,
		Timestamp: time.Now(),
	}

	filter := bson.M{"product_url": productURL}
	update := bson.M{"$push": bson.M{"price_history": newPrice}}

	_, err := m.collection.UpdateOne(context.Background(), filter, update)
	return err
}

func (m *MongoDB) GetProduct(productURL string) (*Product, error) {
	var product Product

	err := m.collection.FindOne(
		context.Background(),
		bson.M{"product_url": productURL},
	).Decode(&product)

	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (m *MongoDB) Close() {
	m.client.Disconnect(context.Background())
}

func (m *MongoDB) UpdatePrices() error {
	// Get all products
	cursor, err := m.collection.Find(context.Background(), bson.M{})
	if err != nil {
		return fmt.Errorf("failed to find products: %v", err)
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var product Product
		if err := cursor.Decode(&product); err != nil {
			log.Printf("Error decoding product: %v\n", err)
			continue
		}

		// Get current price based on URL
		var currentPrice float64
		var err error

		if strings.Contains(product.ProductURL, "flipkart") {
			priceStr, err := ScrapePriceFlipkart(product.ProductURL)
			if err != nil {
				log.Printf("Error scraping Flipkart price for %s: %v\n", product.ProductURL, err)
				continue
			}
			currentPrice, err = strconv.ParseFloat(priceStr, 64)
			if err != nil {
				log.Printf("Error parsing price %s: %v\n", priceStr, err)
				continue
			}
		} else if strings.Contains(product.ProductURL, "amazon") {
			priceStr, err := ScrapePriceAmazon(product.ProductURL)
			if err != nil {
				log.Printf("Error scraping Amazon price for %s: %v\n", product.ProductURL, err)
				continue
			}
			currentPrice, err = strconv.ParseFloat(priceStr, 64)
			if err != nil {
				log.Printf("Error parsing price %s: %v\n", priceStr, err)
				continue
			}
		} else {
			log.Printf("Unsupported vendor for URL: %s\n", product.ProductURL)
			continue
		}

		// Create new price entry
		newPrice := Price{
			Value:     currentPrice,
			Timestamp: time.Now(),
		}

		// Update the product with new price
		update := bson.M{
			"$push": bson.M{
				"price_history": newPrice,
			},
		}

		_, err = m.collection.UpdateOne(
			context.Background(),
			bson.M{"product_url": product.ProductURL},
			update,
		)
		if err != nil {
			log.Printf("Error updating price for %s: %v\n", product.ProductURL, err)
			continue
		}

		fmt.Printf("Updated price for %s: %.2f\n", product.ProductName, currentPrice)
	}

	return nil
}

// // Optionally verify updates
// products, err := db.GetAllProducts()
// if err != nil {
// 	log.Fatalf("Failed to get products: %v", err)
// }

// 	fmt.Printf("\nVerifying price updates for %d products:\n", len(products))
// 	for _, product := range products {
// 		if len(product.PriceHistory) > 0 {
// 			latestPrice := product.PriceHistory[len(product.PriceHistory)-1]
// 			fmt.Printf("%s: Latest price %.2f at %s\n",
// 				product.ProductName,
// 				latestPrice.Value,
// 				latestPrice.Timestamp.Format(time.RFC3339))
// 		}
// 	}
// }

func (m *MongoDB) UpdateIncompleteRecords() error {

	db, err := NewMongoDB(MongoURI, "cypher")
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer db.Close()

	fmt.Println("\nProcessing incomplete records...")
	cursor, err := db.collection.Find(context.Background(), bson.M{
		"$or": []bson.M{
			{"product_name": ""},
			{"product_name": bson.M{"$exists": false}},
			{"image_url": ""},
			{"image_url": bson.M{"$exists": false}},
			{"specifications": bson.M{"$size": 0}},
			{"specifications": bson.M{"$exists": false}},
		},
	})
	if err != nil {
		log.Fatalf("Error finding incomplete records: %v", err)
	}
	defer cursor.Close(context.Background())

	// Process each incomplete record
	for cursor.Next(context.Background()) {
		var product Product
		if err := cursor.Decode(&product); err != nil {
			log.Printf("Error decoding product: %v\n", err)
			continue
		}

		url := (product.ProductURL)
		// var updatedProduct Product

		updatedProduct, err := scrapeProductDetails(url)

		if err != nil {
			log.Printf("Error updating product %s: %v\n", product.ProductURL, err)
			continue
		}

		// Update the product in database
		err = db.UpsertProduct(updatedProduct)
		if err != nil {
			log.Printf("Error updating product %s: %v\n", product.ProductURL, err)
			continue
		}
		fmt.Printf("Successfully updated product: %s\n", product.ProductURL)
	}
	return nil
}

// Verify updates by getting all products
// 	fmt.Println("\nVerifying updates...")
// 	products, err := db.GetAllProducts()
// 	if err != nil {
// 		log.Fatalf("Error getting all products: %v", err)
// 	}

// 	fmt.Printf("\nFound %d products:\n", len(products))
// 	for i, product := range products {
// 		fmt.Printf("\n--- Product %d ---\n", i+1)
// 		fmt.Printf("URL: %s\n", product.ProductURL)
// 		fmt.Printf("Name: %s\n", product.ProductName)
// 		fmt.Printf("Image: %s\n", product.ImageURL)
// 		fmt.Printf("Specifications: %+v\n", product.Specifications)
// 	}
// }
