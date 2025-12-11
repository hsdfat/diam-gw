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
	AVPs     map[string]*AVPDefinition
	Commands []*CommandDefinition
}

// NewProtoParser creates a new parser
func NewProtoParser() *ProtoParser {
	return &ProtoParser{
		AVPs:     make(map[string]*AVPDefinition),
		Commands: make([]*CommandDefinition, 0),
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

		// Skip syntax, package, and option declarations
		if strings.HasPrefix(line, "syntax") ||
			strings.HasPrefix(line, "package") ||
			strings.HasPrefix(line, "option") {
			continue
		}

		// Detect start of AVP or command block
		if strings.HasPrefix(line, "avp ") {
			currentBlock = "avp"
			blockLines = []string{line}
		} else if strings.HasPrefix(line, "command ") {
			currentBlock = "command"
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
				}
				currentBlock = ""
				blockLines = nil
			}
		}
	}

	return scanner.Err()
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
		Name: name,
	}

	// Parse AVP properties
	for i := 1; i < len(lines)-1; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "//") {
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
	avp, ok := p.AVPs[avpName]
	if !ok {
		return fmt.Errorf("undefined AVP: %s", avpName)
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
