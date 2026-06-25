package schema

import (
	"fmt"
	"regexp"
	"strings"
)

func stripCommentPrefix(line string) string {
	before, after, found := strings.Cut(line, "--")
	if !found {
		return line
	}
	return before + strings.TrimPrefix(after, " ")
}

func netParenDepth(line string) int {
	depth := 0
	inSingle := false
	inDouble := false
	for _, ch := range line {
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '(':
			if !inSingle && !inDouble {
				depth++
			}
		case ')':
			if !inSingle && !inDouble {
				depth--
			}
		}
	}
	return depth
}

var driftOpenRE = regexp.MustCompile(`(?i)^\s*--\s*DRIFT\s+(\w+)\s+(DROP|DEPRECATED)\s*\(\s*$`)
var driftCloseRE = regexp.MustCompile(`^\s*--\s*\)\s*;?\s*$`)

func extractDriftBlocks(rawSQL, tableSchema, tableName string) ([]DriftBlock, error) {
	lines := strings.Split(rawSQL, "\n")
	var blocks []DriftBlock
	seenIDs := make(map[string]DriftPolicy)

	scope := DriftScopeFile
	if tableSchema != "" || tableName != "" {
		scope = DriftScopeTable
	}

	inBlock := false
	var currentID string
	var currentPolicy DriftPolicy
	var bodyLines []string
	depth := 0

	for _, line := range lines {
		if !inBlock {
			m := driftOpenRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			id := m[1]
			policy := DriftPolicy(strings.ToUpper(m[2]))
			if existingPolicy, ok := seenIDs[id]; ok && existingPolicy != policy {
				return nil, fmt.Errorf("DRIFT block %q has conflicting policies: %s and %s", id, existingPolicy, policy)
			}
			seenIDs[id] = policy
			inBlock = true
			currentID = id
			currentPolicy = policy
			bodyLines = nil
			depth = 1
		} else {
			strippedLine := stripCommentPrefix(line)
			isClosing := driftCloseRE.MatchString(line)
			net := netParenDepth(strippedLine)
			newDepth := depth + net
			if newDepth == 0 {
				if isClosing {
					blocks = append(blocks, DriftBlock{
						ID:          currentID,
						Policy:      currentPolicy,
						RawBody:     strings.Join(bodyLines, "\n"),
						Scope:       scope,
						TableSchema: tableSchema,
						TableName:   tableName,
					})
					inBlock = false
					continue
				}
				return nil, fmt.Errorf("DRIFT block %q: depth reached 0 before closing -- )", currentID)
			}
			if newDepth < 0 {
				return nil, fmt.Errorf("DRIFT block %q: negative paren depth", currentID)
			}
			bodyLines = append(bodyLines, strippedLine)
			depth = newDepth
		}
	}
	if inBlock {
		return nil, fmt.Errorf("DRIFT block %q: unclosed block (missing -- ))", currentID)
	}
	return blocks, nil
}

func buildAnticipatedDrift(block *DriftBlock) error {
	switch block.Scope {
	case DriftScopeTable:
		body := block.RawBody
		// Strip trailing comma from last non-empty line
		bodyLines := strings.Split(body, "\n")
		for i := len(bodyLines) - 1; i >= 0; i-- {
			if strings.TrimSpace(bodyLines[i]) != "" {
				bodyLines[i] = strings.TrimRight(bodyLines[i], " \t,")
				break
			}
		}
		body = strings.Join(bodyLines, "\n")
		syntheticSQL := "CREATE TABLE __drift_probe__ (\n" + body + "\n);"
		tables, _, _, err := parseDDL(syntheticSQL)
		if err != nil {
			return fmt.Errorf("buildAnticipatedDrift: parse table-scope body for block %q: %w", block.ID, err)
		}
		if len(tables) > 0 {
			block.AnticipatedTable = tables[0]
		}
	case DriftScopeFile:
		tables, indexes, _, err := parseDDL(block.RawBody)
		if err != nil {
			return fmt.Errorf("buildAnticipatedDrift: parse file-scope body for block %q: %w", block.ID, err)
		}
		if len(tables) > 0 {
			block.AnticipatedTable = tables[0]
		}
		block.AnticipatedIndexes = indexes
	}
	return nil
}

// MergeDriftGroups merges all DriftBlocks with the same ID into a single DriftGroup.
func MergeDriftGroups(allBlocks []DriftBlock) (map[string]*DriftGroup, error) {
	groups := make(map[string]*DriftGroup)
	for _, b := range allBlocks {
		g, ok := groups[b.ID]
		if !ok {
			g = &DriftGroup{ID: b.ID, Policy: b.Policy}
			groups[b.ID] = g
		}
		if g.Policy != b.Policy {
			return nil, fmt.Errorf("DRIFT group %q has conflicting policies: %s and %s", b.ID, g.Policy, b.Policy)
		}
		if b.AnticipatedTable != nil {
			g.AnticipatedColumns = append(g.AnticipatedColumns, b.AnticipatedTable.Columns...)
			g.AnticipatedUniqueKeys = append(g.AnticipatedUniqueKeys, b.AnticipatedTable.UniqueKeys...)
			g.AnticipatedForeignKeys = append(g.AnticipatedForeignKeys, b.AnticipatedTable.ForeignKeys...)
		}
		g.AnticipatedIndexes = append(g.AnticipatedIndexes, b.AnticipatedIndexes...)
	}
	return groups, nil
}
