package main

import (
	"net/http"
	"net/url"
	"time"
)

type LGLocation struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Facility string `json:"facility"`
	Domain   string `json:"domain"`
	IPv4     string `json:"ipv4"`
	IPv6     string `json:"ipv6"`
}

type LGProduct struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	CPU     string  `json:"cpu"`
	RAMGB   int     `json:"ramGb"`
	Storage string  `json:"storage"`
	Price   float64 `json:"price"`
	Stock   int     `json:"stock"`
	Checked bool    `json:"checked"`
	BuyURL  string  `json:"buyUrl"`
}

type LGLocationView struct {
	LGLocation
	Total     int         `json:"total"`
	Available int         `json:"available"`
	Products  []LGProduct `json:"products"`
}

type LGStockResponse struct {
	GeneratedAt time.Time        `json:"generatedAt"`
	LastCheck   *CheckRecord     `json:"lastCheck"`
	Summary     Summary          `json:"summary"`
	Locations   []LGLocationView `json:"locations"`
	AffiliateID string           `json:"affiliateId"`
}

var lgLocations = []LGLocation{
	{Code: "ny", Name: "New York City, New York", Facility: "Telehouse", Domain: "https://ny-lg.shockhosting.com", IPv4: "107.161.50.3", IPv6: "2607:a680:0:1::7476"},
	{Code: "la", Name: "Los Angeles, California", Facility: "CoreSite LA2", Domain: "https://la-lg.shockhosting.com", IPv4: "199.87.210.4", IPv6: "2607:a680:1:9::7476"},
	{Code: "il", Name: "Chicago, Illinois", Facility: "CoreSite CH1", Domain: "https://il-lg.shockhosting.com", IPv4: "104.36.231.3", IPv6: "2607:a680:2:1::7476"},
	{Code: "tx", Name: "Dallas, Texas", Facility: "Equinix DA6", Domain: "https://tx-lg.shockhosting.com", IPv4: "172.96.139.3", IPv6: "2607:a680:3:1::7476"},
	{Code: "fl", Name: "Jacksonville, Florida", Facility: "SBA Edge", Domain: "https://fl-lg.shockhosting.com", IPv4: "104.225.131.3", IPv6: "2607:a680:4:1::7476"},
	{Code: "co", Name: "Denver, Colorado", Facility: "CoreSite DE1", Domain: "https://co-lg.shockhosting.com", IPv4: "216.120.203.3", IPv6: "2607:a680:6:1::7476"},
	{Code: "wa", Name: "Seattle, Washington", Facility: "H5 Datacenters", Domain: "https://wa-lg.shockhosting.com", IPv4: "209.182.227.3", IPv6: "2607:a680:9:1::7476"},
	{Code: "uk", Name: "Coventry, England", Facility: "Continuity House", Domain: "https://uk-lg.shockhosting.com", IPv4: "45.155.39.3", IPv6: "2a0d:d8c0:0:1::7476"},
	{Code: "fr", Name: "Paris, France", Facility: "Digital Realty PAR5", Domain: "https://fr-lg.shockhosting.com", IPv4: "193.17.59.3", IPv6: "2a0d:9ec3:0:1::7476"},
	{Code: "nl", Name: "Amsterdam, Netherlands", Facility: "Equinix AM5", Domain: "https://nl-lg.shockhosting.com", IPv4: "217.195.155.3", IPv6: "2a0d:9ec0:0:1::7476"},
	{Code: "de", Name: "Frankfurt, Germany", Facility: "Telehouse", Domain: "https://de-lg.shockhosting.com", IPv4: "213.139.207.3", IPv6: "2a0d:9ec1:0:1::7476"},
	{Code: "ro", Name: "Bucharest, Romania", Facility: "NXDATA-2", Domain: "https://ro-lg.shockhosting.com", IPv4: "185.93.223.3", IPv6: "2a0d:9ec2:0:1::7476"},
	{Code: "au", Name: "Sydney, Australia", Facility: "Equinix SY4", Domain: "https://au-lg.shockhosting.com", IPv4: "203.96.179.3", IPv6: "2406:CB41:0:1::7476"},
	{Code: "jp", Name: "Tokyo, Japan", Facility: "Equinix TY8", Domain: "https://jp-lg.shockhosting.com", IPv4: "43.230.163.3", IPv6: "2406:CB42:0:1::7476"},
	{Code: "sg", Name: "Singapore", Facility: "Equinix SG3", Domain: "https://sg-lg.shockhosting.com", IPv4: "103.159.64.3", IPv6: "2406:CB40:0:1::7476"},
	{Code: "br", Name: "São Paulo, Brazil", Facility: "Ascenty SP4", Domain: "https://br-lg.shockhosting.com", IPv4: "168.222.251.3", IPv6: "2a0d:9ec4:0:1::7476"},
	{Code: "za", Name: "Johannesburg, South Africa", Facility: "Teraco JB1", Domain: "https://za-lg.shockhosting.com", IPv4: "85.155.115.3", IPv6: "2a0d:9ec5:0:1::7476"},
}

func (h *Handler) publicLGStock(w http.ResponseWriter, r *http.Request) {
	state, err := h.app.StockState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取库存失败")
		return
	}

	byLocation := make(map[string][]ProductView)
	for _, product := range state.Products {
		byLocation[product.LocationCode] = append(byLocation[product.LocationCode], product)
	}
	locations := make([]LGLocationView, 0, len(lgLocations))
	for _, location := range lgLocations {
		products := byLocation[location.Code]
		view := LGLocationView{
			LGLocation: location,
			Total:      len(products),
			Available:  0,
			Products:   make([]LGProduct, 0, len(products)),
		}
		for _, product := range products {
			if product.Stock > 0 {
				view.Available++
			}
			view.Products = append(view.Products, LGProduct{
				ID:      product.ID,
				Name:    product.Name,
				CPU:     product.CPU,
				RAMGB:   product.RAMGB,
				Storage: product.Storage,
				Price:   product.PriceMonthly,
				Stock:   product.Stock,
				Checked: product.Checked,
				BuyURL:  affiliateProductURL(h.affiliateID, product.ID),
			})
		}
		locations = append(locations, view)
	}

	writeJSON(w, http.StatusOK, LGStockResponse{
		GeneratedAt: time.Now().UTC(),
		LastCheck:   state.LastCheck,
		Summary:     state.Summary,
		Locations:   locations,
		AffiliateID: h.affiliateID,
	})
}

func affiliateProductURL(affiliateID, productID string) string {
	query := url.Values{}
	query.Set("aff", affiliateID)
	query.Set("pid", productID)
	query.Set("billingcycle", "monthly")
	return "https://shockhosting.com/portal/aff.php?" + query.Encode()
}
