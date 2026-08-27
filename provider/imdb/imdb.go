/*
 * Copyright © 2020 Mário Franco
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in
 * all copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package imdb

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-resty/resty/v2"
	"github.com/lightglitch/seekerr/provider"
	"github.com/rs/zerolog"
)

func NewProvider(logger *zerolog.Logger, restyClient *resty.Client) *Provider {
	return &Provider{
		restyClient: restyClient,
		logger:      logger.With().Str("Component", "IMDB Provider").Logger(),
	}
}

type Provider struct {
	logger      zerolog.Logger
	restyClient *resty.Client
}

func (p *Provider) GetItems(config provider.ListConfig) ([]provider.ListItem, error) {
	limit := config.Filter.Limit

	if limit == 0 {
		limit = 1000
	}

	result := []provider.ListItem{}

	resp, err := p.restyClient.R().
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36").
		SetHeader("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8").
		SetHeader("Accept-Language", "en-GB,en;q=0.9").
		SetDoNotParseResponse(true).
		Get(config.Url)

	if err != nil {
		p.logger.Error().Err(err).Msg("Fetching IMDb HTML")
		return result, err
	}

	if resp.IsError() {
		return result, errors.New(resp.Status() + " - " + resp.String())
	}

	defer resp.RawBody().Close()

	doc, err := goquery.NewDocumentFromReader(resp.RawBody())
	if err != nil {
		p.logger.Error().Err(err).Msg("Parsing IMDb HTML")
		return result, err
	}

	// IMDb's current list pages use:
	//
	//   li.ipc-metadata-list-summary-item
	//
	// rather than the old:
	//
	//   div.lister-list .lister-item
	//
	// Keep the old selector as a fallback in case IMDb serves the legacy
	// layout to some clients.

	items := doc.Find("li.ipc-metadata-list-summary-item")

	if items.Length() == 0 {
		p.logger.Debug().Msg("Current IMDb selectors found no items; trying legacy selectors")
		items = doc.Find("div.lister-list .lister-item")
	}

	if items.Length() == 0 {
		p.logger.Warn().Msg("No IMDb list items found")
		return result, nil
	}

	p.logger.Debug().Int("Items", items.Length()).Msg("IMDb list items found")

	idRegex := regexp.MustCompile(`tt\d+`)
	yearRegex := regexp.MustCompile(`(?:19|20)\d{2}`)

	items.EachWithBreak(func(index int, s *goquery.Selection) bool {
		if index >= limit {
			return false
		}

		var title string
		var imdbID string
		var year int

		// ------------------------------------------------------------
		// Current IMDb layout
		// ------------------------------------------------------------

		titleLink := s.Find("a.ipc-title-link-wrapper").First()

		if titleLink.Length() == 0 {
			titleLink = s.Find(`a[href*="/title/tt"]`).First()
		}

		if titleLink.Length() > 0 {
			href := titleLink.AttrOr("href", "")

			if href != "" {
				imdbID = idRegex.FindString(href)
			}
		}

		titleElement := s.Find("h3.ipc-title__text").First()

		if titleElement.Length() > 0 {
			title = strings.TrimSpace(titleElement.Text())

			// IMDb often prefixes titles with the list rank:
			//
			// 1. The Shawshank Redemption
			//
			// Remove that prefix because Seekerr only wants the title.
			title = regexp.MustCompile(`^\d+\.\s*`).ReplaceAllString(title, "")
		}

		// Current IMDb list pages expose metadata as:
		//
		// <span class="cli-title-metadata-item">1994</span>
		//
		// Use the first four-digit year we can find.
		metadata := s.Find("span.cli-title-metadata-item")

		metadata.EachWithBreak(func(_ int, m *goquery.Selection) bool {
			text := strings.TrimSpace(m.Text())

			match := yearRegex.FindString(text)

			if match != "" {
				year, _ = strconv.Atoi(match)
				return false
			}

			return true
		})

		// ------------------------------------------------------------
		// Legacy IMDb layout fallback
		// ------------------------------------------------------------

		if title == "" {
			titleElement = s.Find(".lister-item-header a").First()

			if titleElement.Length() > 0 {
				title = strings.TrimSpace(titleElement.Text())
			}
		}

		if year == 0 {
			yearText := s.Find(".lister-item-header .lister-item-year").First().Text()

			if match := yearRegex.FindString(yearText); match != "" {
				year, _ = strconv.Atoi(match)
			}
		}

		if imdbID == "" {
			href := s.Find(".lister-item-header a").First().AttrOr("href", "")

			if href != "" {
				imdbID = idRegex.FindString(href)
			}
		}

		// Don't add malformed entries. An IMDb ID is particularly
		// important because Seekerr uses it when resolving the movie.
		if imdbID == "" {
			p.logger.Warn().
				Str("Title", title).
				Int("Year", year).
				Msg("Skipping IMDb list item because no IMDb ID was found")

			return true
		}

		p.logger.Debug().
			Str("ImdbId", imdbID).
			Str("Title", title).
			Int("Year", year).
			Msg("Processing IMDb list item")

		result = append(result, provider.ListItem{
			Title: title,
			Year:  year,
			Imdb:  imdbID,
		})

		return true
	})

	p.logger.Info().
		Int("Items", len(result)).
		Msg("IMDb list processing complete")

	return result, nil
}
