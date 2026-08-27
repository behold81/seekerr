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
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"

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

	// IMDb's HTML list pages are now client-rendered. The /export
	// endpoint provides the same list as CSV and is considerably more
	// stable for programmatic access.
	exportURL := strings.TrimRight(config.Url, "/") + "/export"

	p.logger.Debug().
		Str("URL", exportURL).
		Msg("Fetching IMDb list export")

	resp, err := p.restyClient.R().
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36").
		SetHeader("Accept", "text/csv,text/plain,*/*").
		SetHeader("Accept-Language", "en-GB,en;q=0.9").
		SetDoNotParseResponse(true).
		Get(exportURL)

	if err != nil {
		p.logger.Error().Err(err).Msg("Fetching IMDb list export")
		return result, err
	}

	if resp.IsError() {
		return result, errors.New(resp.Status() + " - " + resp.String())
	}

	defer resp.RawBody().Close()

	reader := csv.NewReader(resp.RawBody())
	reader.FieldsPerRecord = -1

	// Read CSV header.
	header, err := reader.Read()
	if err != nil {
		p.logger.Error().Err(err).Msg("Reading IMDb CSV header")
		return result, err
	}

	columns := make(map[string]int)

	for i, column := range header {
		columns[strings.ToLower(strings.TrimSpace(column))] = i
	}

	// IMDb's export uses "Const" for the IMDb title ID.
	constIndex, ok := columns["const"]
	if !ok {
		return result, errors.New("IMDb CSV does not contain a Const column")
	}

	titleIndex, titleOK := columns["title"]
	yearIndex, yearOK := columns["year"]

	if !titleOK {
		return result, errors.New("IMDb CSV does not contain a Title column")
	}

	p.logger.Debug().
		Int("Columns", len(header)).
		Msg("IMDb CSV header loaded")

	for index := 0; index < limit; index++ {
		record, err := reader.Read()

		if err == io.EOF {
			break
		}

		if err != nil {
			p.logger.Warn().
				Err(err).
				Msg("Skipping malformed IMDb CSV record")
			continue
		}

		if constIndex >= len(record) {
			continue
		}

		imdbID := strings.TrimSpace(record[constIndex])

		if !strings.HasPrefix(imdbID, "tt") {
			continue
		}

		title := ""

		if titleIndex < len(record) {
			title = strings.TrimSpace(record[titleIndex])
		}

		year := 0

		if yearOK && yearIndex < len(record) {
			yearText := strings.TrimSpace(record[yearIndex])

			if yearText != "" && yearText != "\\N" {
				year, _ = strconv.Atoi(yearText)
			}
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
	}

	p.logger.Info().
		Int("Items", len(result)).
		Msg("IMDb list processing complete")

	return result, nil
}
