package archive

func copyBoolMap(input map[string]bool) map[string]bool {
	output := map[string]bool{}
	for key, value := range input {
		output[key] = value
	}
	return output
}

func copyDigestMap(input map[string]ProcessingDigest) map[string]ProcessingDigest {
	output := map[string]ProcessingDigest{}
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneForMutation(value *InterviewArchive) InterviewArchive {
	clone := *value
	clone.Segments = append([]TranscriptSegment(nil), value.Segments...)
	clone.Marks = append([]SensitivityMark(nil), value.Marks...)
	clone.Affected = copyBoolMap(value.Affected)
	clone.ProcessedDigests = copyDigestMap(value.ProcessedDigests)
	return clone
}
