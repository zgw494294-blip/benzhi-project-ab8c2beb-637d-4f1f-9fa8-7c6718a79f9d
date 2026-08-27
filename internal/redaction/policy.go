package redaction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

type PolicyAssessment struct {
	ConsentPresent bool         `json:"consentPresent"`
	Rules          []PolicyRule `json:"rules"`
	Issues         []Issue      `json:"issues"`
	Digest         string       `json:"digest"`
}

type PolicyRule struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

func AssessPolicy(a *archive.InterviewArchive) PolicyAssessment {
	assessment := PolicyAssessment{ConsentPresent: a.Consent != nil, Rules: []PolicyRule{}, Issues: []Issue{}}
	if a.Consent == nil {
		assessment.Issues = append(assessment.Issues, Issue{Code: "consent_missing", Message: "授权约束尚未登记"})
		assessment.Digest = policyDigest(assessment)
		return assessment
	}
	assessment.Rules = append(assessment.Rules, disclosureRule(a.Consent.NameDisclosure))
	if len(a.Consent.RestrictedTopics) > 0 {
		assessment.Rules = append(assessment.Rules, PolicyRule{Code: "restricted_topic_removal", Description: "受限主题标注必须删除，不得保留替代叙述", Source: strings.Join(sortedStrings(a.Consent.RestrictedTopics), "、")})
	}
	for _, mark := range a.Marks {
		segment, ok := a.Segment(mark.SegmentID)
		if !ok {
			assessment.Issues = append(assessment.Issues, Issue{SegmentID: mark.SegmentID, MarkID: mark.ID, Code: "segment_missing", Message: "标注引用的段落不存在"})
			continue
		}
		assessment.Issues = append(assessment.Issues, assessMark(*a.Consent, segment, mark)...)
	}
	sort.Slice(assessment.Rules, func(i, j int) bool { return assessment.Rules[i].Code < assessment.Rules[j].Code })
	sort.Slice(assessment.Issues, func(i, j int) bool {
		if assessment.Issues[i].SegmentID == assessment.Issues[j].SegmentID {
			if assessment.Issues[i].MarkID == assessment.Issues[j].MarkID {
				return assessment.Issues[i].Code < assessment.Issues[j].Code
			}
			return assessment.Issues[i].MarkID < assessment.Issues[j].MarkID
		}
		return assessment.Issues[i].SegmentID < assessment.Issues[j].SegmentID
	})
	assessment.Digest = policyDigest(assessment)
	return assessment
}

func disclosureRule(disclosure archive.NameDisclosure) PolicyRule {
	switch disclosure {
	case archive.DisclosureForbidden:
		return PolicyRule{Code: "identity_hidden", Description: "人物身份必须删除或替换为不可识别代称", Source: string(disclosure)}
	case archive.DisclosurePseudonym:
		return PolicyRule{Code: "identity_pseudonym", Description: "人物身份仅可替换为代号或删除", Source: string(disclosure)}
	default:
		return PolicyRule{Code: "identity_allowed", Description: "授权允许披露姓名，已标注身份仍按登记策略处置", Source: string(disclosure)}
	}
}

func assessMark(consent archive.ConsentScope, segment archive.TranscriptSegment, mark archive.SensitivityMark) []Issue {
	issues := []Issue{}
	if mark.Category == archive.CategoryIdentity && consent.NameDisclosure != archive.DisclosureAllowed {
		if mark.Strategy == archive.StrategyGeneralize {
			issues = append(issues, Issue{SegmentID: mark.SegmentID, MarkID: mark.ID, Code: "identity_strategy", Message: "当前姓名披露规则要求删除身份或替换为明确代号"})
		}
	}
	if mark.Category == archive.CategoryTopic && len(consent.RestrictedTopics) > 0 && mark.Strategy != archive.StrategyDelete {
		issues = append(issues, Issue{SegmentID: mark.SegmentID, MarkID: mark.ID, Code: "restricted_topic_retained", Message: "授权列明禁用主题，此类标注必须使用删除策略"})
	}
	if mark.Strategy == archive.StrategyReplace || mark.Strategy == archive.StrategyGeneralize {
		runes := []rune(segment.SourceText)
		if mark.StartOffset >= 0 && mark.EndOffset <= len(runes) && mark.StartOffset < mark.EndOffset {
			original := strings.TrimSpace(string(runes[mark.StartOffset:mark.EndOffset]))
			if original != "" && original == strings.TrimSpace(mark.Replacement) {
				issues = append(issues, Issue{SegmentID: mark.SegmentID, MarkID: mark.ID, Code: "ineffective_replacement", Message: "处置文本与敏感原文相同，未产生脱敏效果"})
			}
		}
	}
	return issues
}

func policyDigest(assessment PolicyAssessment) string {
	payload := struct {
		ConsentPresent bool         `json:"consentPresent"`
		Rules          []PolicyRule `json:"rules"`
		Issues         []Issue      `json:"issues"`
	}{assessment.ConsentPresent, assessment.Rules, assessment.Issues}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
