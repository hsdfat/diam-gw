package codegen

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hsdfat8/diam-gw/models_base"
)

// ProtoParser parses diameter proto files
type ProtoParser struct {
	AVPs        map[string]*AVPDefinition
	Commands    []*CommandDefinition
	Consts      map[string]uint64         // Protocol-level constants
	Enums       map[string]*EnumDefinition // Enum definitions
	Package     string                     // Proto package name (e.g., "diameter.base")
	GoPackage   string                     // Go package path (e.g., "github.com/hsdfat8/diam-gw/commands/base")
	PackageName string                     // Simple package name extracted from go_package (e.g., "base")
}

// EnumDefinition represents an enum type definition
type EnumDefinition struct {
	Name   string
	Values map[string]uint32 // Value name -> Value
}

// EnumValue represents a single enum value
type EnumValue struct {
	Name  string
	Value uint32
}

// NewProtoParser creates a new parser
func NewProtoParser() *ProtoParser {
	return &ProtoParser{
		AVPs:     make(map[string]*AVPDefinition),
		Commands: make([]*CommandDefinition, 0),
		Consts:   make(map[string]uint64),
		Enums:    make(map[string]*EnumDefinition),
	}
}

// ParseFile parses a diameter proto file
func (p *ProtoParser) ParseFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var currentBlock string
	var blockLines []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Parse syntax, package, and option declarations
		if strings.HasPrefix(line, "syntax") {
			continue
		}

		if strings.HasPrefix(line, "package ") {
			// Extract package name: "package diameter.base;"
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				p.Package = strings.TrimSuffix(parts[1], ";")
			}
			continue
		}

		if strings.HasPrefix(line, "option go_package") {
			// Extract go_package: option go_package = "github.com/hsdfat8/diam-gw/commands/base";
			parts := strings.Split(line, "=")
			if len(parts) >= 2 {
				goPackage := strings.TrimSpace(parts[1])
				goPackage = strings.Trim(goPackage, "\"';")
				p.GoPackage = goPackage

				// Extract simple package name from go_package path
				pathParts := strings.Split(goPackage, "/")
				if len(pathParts) > 0 {
					p.PackageName = pathParts[len(pathParts)-1]
				}
			}
			continue
		}

		// Parse const declarations: const S6A_APPLICATION_ID = 16777251;
		if strings.HasPrefix(line, "const ") {
			if err := p.parseConst(line); err != nil {
				return err
			}
			continue
		}

		// Detect start of AVP, command, or enum block
		if strings.HasPrefix(line, "avp ") {
			currentBlock = "avp"
			blockLines = []string{line}
		} else if strings.HasPrefix(line, "command ") {
			currentBlock = "command"
			blockLines = []string{line}
		} else if strings.HasPrefix(line, "enum ") {
			currentBlock = "enum"
			blockLines = []string{line}
		} else if currentBlock != "" {
			blockLines = append(blockLines, line)

			// Detect end of block
			if line == "}" {
				if currentBlock == "avp" {
					if err := p.parseAVPBlock(blockLines); err != nil {
						return err
					}
				} else if currentBlock == "command" {
					if err := p.parseCommandBlock(blockLines); err != nil {
						return err
					}
				} else if currentBlock == "enum" {
					if err := p.parseEnumBlock(blockLines); err != nil {
						return err
					}
				}
				currentBlock = ""
				blockLines = nil
			}
		}
	}

	return scanner.Err()
}

// ResolveAVPReferences resolves AVP references in commands and grouped fields
// This should be called after all files have been parsed
func (p *ProtoParser) ResolveAVPReferences() {
	// Resolve references in command fields
	for _, cmd := range p.Commands {
		for _, field := range cmd.Fields {
			if field.AVP != nil && field.AVP.Name != "AVP" {
				// Try to resolve from AVPs map - always attempt resolution
				if resolvedAVP, ok := p.AVPs[field.AVP.Name]; ok && resolvedAVP.Code != 0 {
					field.AVP = resolvedAVP
				}
			}
		}
	}

	// Resolve references in grouped fields
	for _, avp := range p.AVPs {
		for _, field := range avp.GroupedFields {
			if field.AVP != nil && field.AVP.Name != "AVP" {
				// Try to resolve from AVPs map - always attempt resolution
				if resolvedAVP, ok := p.AVPs[field.AVP.Name]; ok && resolvedAVP.Code != 0 {
					field.AVP = resolvedAVP
				}
			}
		}
	}
}

// parseAVPBlock parses an AVP definition block
func (p *ProtoParser) parseAVPBlock(lines []string) error {
	if len(lines) == 0 {
		return fmt.Errorf("empty AVP block")
	}

	// Parse AVP name from first line: "avp Origin-Host {"
	firstLine := strings.TrimSpace(lines[0])
	parts := strings.Fields(firstLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid AVP declaration: %s", firstLine)
	}

	name := strings.TrimSuffix(parts[1], "{")
	name = strings.TrimSpace(name)

	avp := &AVPDefinition{
		Name:          name,
		GroupedFields: make([]*AVPField, 0),
	}

	// Parse AVP properties
	inGroupedBlock := false
	var groupedLines []string

	// Process all lines including the closing brace
	// The last line (lines[len-1]) is always "}" which closes the AVP block
	// We need to process up to but not including that line
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Check for grouped block start
		if strings.HasPrefix(line, "grouped {") {
			inGroupedBlock = true
			groupedLines = []string{}
			continue
		}

		if inGroupedBlock {
			// Check for end of grouped block
			if line == "}" {
				// End of grouped block
				inGroupedBlock = false
				// Parse grouped fields
				if err := p.parseGroupedFields(avp, groupedLines); err != nil {
					return err
				}
				// Continue to next line - this closing brace belongs to grouped block
				continue
			}
			// We're inside grouped block, collect the line
			groupedLines = append(groupedLines, line)
			continue
		}

		// Remove trailing semicolon
		line = strings.TrimSuffix(line, ";")

		// Split by =
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "code":
			code, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return fmt.Errorf("invalid code for AVP %s: %v", name, err)
			}
			avp.Code = uint32(code)
		case "type":
			avp.TypeName = value
			if typeID, ok := models_base.Available[value]; ok {
				avp.Type = typeID
			}
		case "must":
			avp.Must = value == "true"
		case "may_encrypt":
			avp.MayEncrypt = value == "true"
		case "vendor_id":
			vendorID, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return fmt.Errorf("invalid vendor_id for AVP %s: %v", name, err)
			}
			avp.VendorID = uint32(vendorID)
		}
	}

	p.AVPs[name] = avp
	return nil
}

// parseGroupedFields parses the grouped block for a Grouped AVP
func (p *ProtoParser) parseGroupedFields(avp *AVPDefinition, lines []string) error {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Remove inline comments first
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}

		// Remove trailing semicolon
		line = strings.TrimSuffix(line, ";")

		// Parse field definition: "optional Vendor-Id vendor_id = 1"
		// or "required Proxy-Host proxy_host = 1"
		// or "repeated AVP avp = 1"
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}

		field := &AVPField{}
		idx := 0

		// Check for "optional", "required", or "repeated"
		if parts[idx] == "optional" {
			field.Required = false
			idx++
		} else if parts[idx] == "required" {
			field.Required = true
			idx++
		} else if parts[idx] == "repeated" {
			field.Repeated = true
			field.Required = false
			idx++
		}

		// AVP name
		avpName := parts[idx]
		idx++

		// Field name
		fieldName := parts[idx]
		idx++

		// Position (after =)
		if idx < len(parts) && parts[idx] == "=" {
			idx++
			if idx < len(parts) {
				posStr := strings.TrimSuffix(parts[idx], ";")
				pos, err := strconv.Atoi(posStr)
				if err == nil {
					field.Position = pos
				}
			}
		}

		// Get or create AVP definition
		// For grouped fields, we need to look up the AVP
		// If it doesn't exist yet, we'll create a placeholder
		var fieldAVP *AVPDefinition

		// Handle generic "AVP" type (used for extensibility)
		if avpName == "AVP" {
			fieldAVP = &AVPDefinition{
				Name:     "AVP",
				Code:     0, // Will be runtime determined
				TypeName: "OctetString",
			}
		} else {
			var ok bool
			fieldAVP, ok = p.AVPs[avpName]
			if !ok {
				// Create a placeholder - will be resolved later
				fieldAVP = &AVPDefinition{
					Name:     avpName,
					TypeName: "OctetString", // Default placeholder type
				}
			}
		}

		field.AVP = fieldAVP
		field.FieldName = toCamelCase(fieldName)

		avp.GroupedFields = append(avp.GroupedFields, field)
	}

	return nil
}

// parseCommandBlock parses a command definition block
func (p *ProtoParser) parseCommandBlock(lines []string) error {
	if len(lines) == 0 {
		return fmt.Errorf("empty command block")
	}

	// Parse command name from first line: "command Capabilities-Exchange-Request {"
	firstLine := strings.TrimSpace(lines[0])
	parts := strings.Fields(firstLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid command declaration: %s", firstLine)
	}

	name := strings.TrimSuffix(parts[1], "{")
	name = strings.TrimSpace(name)

	cmd := &CommandDefinition{
		Name:   name,
		Fields: make([]*AVPField, 0),
	}

	// Parse command properties
	for i := 1; i < len(lines)-1; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Remove inline comments first
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}

		// Remove trailing semicolon
		line = strings.TrimSuffix(line, ";")

		// Check if it's a field definition or property
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			// Check if this is a field definition (has space before =)
			if strings.Contains(key, " ") {
				// This is a field definition
				if err := p.parseFieldDefinition(cmd, line); err != nil {
					return err
				}
			} else {
				// This is a command property
				switch key {
				case "code":
					code, err := strconv.ParseUint(value, 10, 32)
					if err != nil {
						return fmt.Errorf("invalid code for command %s: %v", name, err)
					}
					cmd.Code = uint32(code)
				case "application_id":
					appID, err := strconv.ParseUint(value, 10, 32)
					if err != nil {
						return fmt.Errorf("invalid application_id for command %s: %v", name, err)
					}
					cmd.ApplicationID = uint32(appID)
				case "request":
					cmd.Request = value == "true"
				case "proxiable":
					cmd.Proxiable = value == "true"
				}
			}
		}
	}

	// Generate abbreviation from name
	cmd.Abbreviation = generateAbbreviation(name)
	// Set the package name for this command
	cmd.Package = p.PackageName

	p.Commands = append(p.Commands, cmd)
	return nil
}

// parseFieldDefinition parses a field definition line
func (p *ProtoParser) parseFieldDefinition(cmd *CommandDefinition, line string) error {
	// Example: "fixed required Origin-Host origin_host = 1;"
	// Example: "repeated optional Auth-Application-Id auth_application_id = 8;"

	parts := strings.Fields(line)
	if len(parts) < 4 {
		return fmt.Errorf("invalid field definition: %s", line)
	}

	field := &AVPField{}
	idx := 0

	// Check for "fixed" or "repeated"
	if parts[idx] == "fixed" {
		field.Fixed = true
		idx++
	} else if parts[idx] == "repeated" {
		field.Repeated = true
		idx++
	}

	// Check for "required" or "optional"
	if parts[idx] == "required" {
		field.Required = true
		idx++
	} else if parts[idx] == "optional" {
		field.Required = false
		idx++
	}

	// AVP name
	avpName := parts[idx]
	idx++

	// Field name
	fieldName := parts[idx]
	idx++

	// Position (after =)
	if idx < len(parts) && parts[idx] == "=" {
		idx++
		if idx < len(parts) {
			posStr := strings.TrimSuffix(parts[idx], ";")
			pos, err := strconv.Atoi(posStr)
			if err == nil {
				field.Position = pos
			}
		}
	}

	// Get AVP definition
	// Handle generic "AVP" type (used for extensibility)
	var avp *AVPDefinition
	if avpName == "AVP" {
		avp = &AVPDefinition{
			Name:     "AVP",
			Code:     0, // Will be runtime determined
			TypeName: "OctetString",
		}
	} else {
		var ok bool
		avp, ok = p.AVPs[avpName]
		if !ok {
			// Create a placeholder for AVPs defined in other proto files (e.g., base protocol AVPs)
			// These will be imported from their respective packages at runtime
			avp = &AVPDefinition{
				Name:     avpName,
				TypeName: "OctetString", // Default placeholder - actual type from imported package
			}
		}
	}

	field.AVP = avp
	field.FieldName = toCamelCase(fieldName)

	cmd.Fields = append(cmd.Fields, field)
	return nil
}

// generateAbbreviation generates command abbreviation from name
func generateAbbreviation(name string) string {
	// Split by dash and take first letter of each word
	parts := strings.Split(name, "-")
	abbr := ""
	for _, part := range parts {
		if len(part) > 0 {
			abbr += strings.ToUpper(string(part[0]))
		}
	}
	return abbr
}

// parseConst parses a const declaration
func (p *ProtoParser) parseConst(line string) error {
	// Example: const S6A_APPLICATION_ID = 16777251;
	line = strings.TrimPrefix(line, "const ")

	// Remove inline comments first
	if idx := strings.Index(line, "//"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}

	line = strings.TrimSuffix(line, ";")

	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid const declaration: %s", line)
	}

	name := strings.TrimSpace(parts[0])
	valueStr := strings.TrimSpace(parts[1])

	value, err := strconv.ParseUint(valueStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid const value for %s: %v", name, err)
	}

	p.Consts[name] = value
	return nil
}

// parseEnumBlock parses an enum definition block
func (p *ProtoParser) parseEnumBlock(lines []string) error {
	if len(lines) == 0 {
		return fmt.Errorf("empty enum block")
	}

	// Parse enum name from first line: "enum CancellationType {"
	firstLine := strings.TrimSpace(lines[0])
	parts := strings.Fields(firstLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid enum declaration: %s", firstLine)
	}

	name := strings.TrimSuffix(parts[1], "{")
	name = strings.TrimSpace(name)

	enum := &EnumDefinition{
		Name:   name,
		Values: make(map[string]uint32),
	}

	// Parse enum values
	for i := 1; i < len(lines)-1; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Remove inline comments first
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}

		// Remove trailing semicolon
		line = strings.TrimSuffix(line, ";")

		// Parse enum value: "MME_UPDATE_PROCEDURE = 0"
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		valueName := strings.TrimSpace(parts[0])
		valueStr := strings.TrimSpace(parts[1])

		value, err := strconv.ParseUint(valueStr, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid enum value for %s.%s: %v", name, valueName, err)
		}

		enum.Values[valueName] = uint32(value)
	}

	p.Enums[name] = enum
	return nil
}

// toCamelCase converts snake_case to CamelCase
func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(string(part[0])) + part[1:]
		}
	}
	return strings.Join(parts, "")
}
