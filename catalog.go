package main

import (
	"fmt"
	"sort"
)

type Product struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	LocationCode string  `json:"locationCode"`
	LocationName string  `json:"locationName"`
	CPU          string  `json:"cpu"`
	RAMGB        int     `json:"ramGb"`
	Storage      string  `json:"storage"`
	StorageType  string  `json:"storageType"`
	PriceMonthly float64 `json:"priceMonthly"`
	OrderURL     string  `json:"orderUrl"`
}

type locationSpec struct {
	Code string
	Name string
	IDs  [4]int
	CPU  string
	RAM  int
}

var locationSpecs = []locationSpec{
	{Code: "ny", Name: "New York", IDs: [4]int{93, 95, 94, 96}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "la", Name: "Los Angeles", IDs: [4]int{89, 91, 90, 92}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "il", Name: "Chicago", IDs: [4]int{81, 83, 82, 84}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "tx", Name: "Dallas", IDs: [4]int{97, 99, 98, 100}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "fl", Name: "Jacksonville", IDs: [4]int{101, 103, 102, 104}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "co", Name: "Denver", IDs: [4]int{113, 115, 114, 116}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "wa", Name: "Seattle", IDs: [4]int{121, 123, 122, 124}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "uk", Name: "Coventry", IDs: [4]int{85, 87, 86, 88}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "nl", Name: "Amsterdam", IDs: [4]int{77, 78, 79, 80}, CPU: "Intel Xeon E3-1271 v3", RAM: 16},
	{Code: "de", Name: "Frankfurt", IDs: [4]int{125, 127, 126, 128}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "fr", Name: "Paris", IDs: [4]int{134, 136, 135, 137}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "ro", Name: "Bucharest", IDs: [4]int{129, 131, 130, 132}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "au", Name: "Sydney", IDs: [4]int{109, 111, 110, 112}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "jp", Name: "Tokyo", IDs: [4]int{117, 119, 118, 120}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "sg", Name: "Singapore", IDs: [4]int{105, 107, 106, 108}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "br", Name: "São Paulo", IDs: [4]int{138, 140, 139, 141}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
	{Code: "za", Name: "Johannesburg", IDs: [4]int{142, 144, 143, 145}, CPU: "Intel Xeon E3-1270 v3", RAM: 32},
}

type driveSpec struct {
	Name  string
	Type  string
	Price float64
}

var driveSpecs = [4]driveSpec{
	{Name: "1 × 2 TB HDD", Type: "hdd", Price: 49.99},
	{Name: "1 × 480 GB SSD", Type: "ssd", Price: 49.99},
	{Name: "2 × 2 TB HDD", Type: "hdd", Price: 59.99},
	{Name: "2 × 480 GB SSD", Type: "ssd", Price: 59.99},
}

var catalog = buildCatalog()
var catalogByID = buildCatalogIndex()

func buildCatalog() []Product {
	products := make([]Product, 0, len(locationSpecs)*4)
	for _, location := range locationSpecs {
		suffixes := [4]string{"DH1", "DS1", "DH2", "DS2"}
		for index, id := range location.IDs {
			drive := driveSpecs[index]
			name := fmt.Sprintf("%s-%s", upperCode(location.Code), suffixes[index])
			products = append(products, Product{
				ID:           fmt.Sprintf("%d", id),
				Name:         name,
				LocationCode: location.Code,
				LocationName: location.Name,
				CPU:          location.CPU,
				RAMGB:        location.RAM,
				Storage:      drive.Name,
				StorageType:  drive.Type,
				PriceMonthly: drive.Price,
				OrderURL: fmt.Sprintf(
					"https://shockhosting.com/portal/cart.php?a=add&pid=%d&billingcycle=monthly",
					id,
				),
			})
		}
	}
	sort.Slice(products, func(i, j int) bool {
		if products[i].LocationName == products[j].LocationName {
			return products[i].Name < products[j].Name
		}
		return products[i].LocationName < products[j].LocationName
	})
	return products
}

func buildCatalogIndex() map[string]Product {
	index := make(map[string]Product, len(catalog))
	for _, product := range catalog {
		index[product.ID] = product
	}
	return index
}

func upperCode(code string) string {
	result := make([]byte, len(code))
	for i := range code {
		c := code[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}
