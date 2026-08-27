package redaction

import (
	"unicode/utf8"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

type Summary struct {
	SegmentCount          int            `json:"segmentCount"`
	MarkCount             int            `json:"markCount"`
	SourceCharacters      int            `json:"sourceCharacters"`
	SanitizedCharacters   int            `json:"sanitizedCharacters"`
	RemovedCharacters     int            `json:"removedCharacters"`
	InsertedCharacters    int            `json:"insertedCharacters"`
	DeletedCharacters     int            `json:"deletedCharacters"`
	ReplacementCharacters int            `json:"replacementCharacters"`
	ByCategory            map[string]int `json:"byCategory"`
	ByStrategy            map[string]int `json:"byStrategy"`
	BySegment             map[string]int `json:"bySegment"`
}

func summarize(results []SegmentResult) Summary {
	summary := Summary{SegmentCount: len(results), ByCategory: map[string]int{}, ByStrategy: map[string]int{}, BySegment: map[string]int{}}
	for _, result := range results {
		summary.SourceCharacters += utf8.RuneCountInString(result.Source)
		summary.SanitizedCharacters += utf8.RuneCountInString(result.Sanitized)
		for _, span := range result.Spans {
			summary.MarkCount++
			summary.ByCategory[span.Category]++
			summary.ByStrategy[span.Strategy]++
			summary.BySegment[result.SegmentID]++
			beforeLength := utf8.RuneCountInString(span.Before)
			afterLength := utf8.RuneCountInString(span.After)
			if beforeLength > afterLength {
				summary.RemovedCharacters += beforeLength - afterLength
			}
			if afterLength > beforeLength {
				summary.InsertedCharacters += afterLength - beforeLength
			}
			if span.Strategy == string(archive.StrategyDelete) {
				summary.DeletedCharacters += beforeLength
			} else {
				summary.ReplacementCharacters += afterLength
			}
		}
	}
	return summary
}
