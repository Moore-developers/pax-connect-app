package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func isRouteTool(name string) bool {
	for _, v := range routeTools {
		if v == name {
			return true
		}
	}
	return false
}
func invalidArgument() error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid_argument"}
}
func (s *Server) registerTool(name, permission string, route bool) error {
	input := inputSchema(name)
	output := outputSchema(name)
	in, err := resolveSchema(input)
	if err != nil {
		return fmt.Errorf("%s input: %w", name, err)
	}
	out, err := resolveSchema(output)
	if err != nil {
		return fmt.Errorf("%s output: %w", name, err)
	}
	description := descriptions[name]
	if isAITool(name) {
		description = aiDescription(name)
	}
	setting := isSettingsTool(name)
	cf := isCFTool(name)
	profile := isProfileTool(name)
	network := isNetworkTool(name)
	readonly := !route || name == "pax_route_get"
	if profile {
		readonly = name == "pax_profiles_list"
		description = profileDescription(name)
	}
	if network {
		readonly = networkReadOnly(name)
		description = networkDescription(name)
	}
	if cf {
		readonly = name == "pax_cf_workspace"
		description = cfDescription(name)
	}
	if route {
		description = routeDescription(name)
	}
	if setting {
		readonly = name == "pax_settings_get"
		description = settingsDescription(name)
	}
	annotation := &mcp.ToolAnnotations{ReadOnlyHint: readonly, OpenWorldHint: boolPointer(name == "pax_routes_preview" || name == "pax_subscription_refresh")}
	if route {
		annotation.DestructiveHint = boolPointer(permission == "delete")
		annotation.IdempotentHint = name != "pax_routes_preview" && name != "pax_routes_preview_cancel"
	}
	if network {
		annotation.ReadOnlyHint = readonly
		annotation.IdempotentHint = !readonly
		annotation.OpenWorldHint = boolPointer(!readonly && name != "pax_operation_cancel")
	}
	if profile {
		annotation.ReadOnlyHint = readonly
		annotation.IdempotentHint = !readonly
		annotation.DestructiveHint = boolPointer(permission == "delete")
		annotation.OpenWorldHint = boolPointer(name == "pax_profile_models_refresh" || name == "pax_profile_save" || name == "pax_profile_import")
	}
	if cf {
		annotation.ReadOnlyHint = readonly
		annotation.IdempotentHint = !readonly
		annotation.OpenWorldHint = boolPointer(name == "pax_cf_run" || name == "pax_cf_validate")
	}
	if setting {
		annotation.ReadOnlyHint = readonly
		annotation.IdempotentHint = !readonly
		annotation.OpenWorldHint = boolPointer(false)
		annotation.DestructiveHint = boolPointer(name == "pax_logs_clear" || name == "pax_logs_retention")
	}
	// Claude Code omits tools with root-level unions before exposing them to
	// the model. Keep the complete schema above for server-side validation and
	// advertise its object fields; nested unions remain supported by clients.
	advertised := make(schema, len(input))
	for key, value := range input {
		advertised[key] = value
	}
	if _, ok := advertised["oneOf"]; ok {
		delete(advertised, "oneOf")
		description += " Mutually exclusive argument variants are checked by the server; supply exactly one variant."
	}
	s.mcp.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: advertised, OutputSchema: output, Annotations: annotation}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !s.allowed[permission] && (!network || permission != "" || !s.canCancel()) {
			return failure("permission_denied", "never"), nil
		}
		args, parseErr := parseArguments(req.Params.Arguments)
		if parseErr != nil {
			return nil, parseErr
		}
		if in.Validate(args) != nil {
			return nil, invalidArgument()
		}
		if err := checkInputBytes(args, input); err != nil {
			return nil, err
		}
		if isAITool(name) {
			if err := validateAIArgs(args); err != nil {
				return nil, err
			}
		}
		if profile {
			if err := validateProfileArgs(args); err != nil {
				return nil, err
			}
		}
		if cf {
			if err := validateCFArgs(name, args); err != nil {
				return nil, err
			}
		}
		if route || network || setting {
			if err := validateRouteArgs(name, args); err != nil {
				return nil, err
			}
		}
		timeout := 5 * time.Second
		if !readonly {
			timeout = 15 * time.Second
		}
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var value map[string]any
		var err error
		if setting {
			value, err = s.settingsCall(bounded, name, args)
		} else if cf && !readonly {
			value, err = s.cfCall(bounded, name, args)
		} else if profile && !readonly {
			value, err = s.profileCall(bounded, name, args)
		} else if network {
			value, err = s.networkCall(bounded, name, args)
		} else if route && name != "pax_routes_preview" {
			value, err = s.routeCall(bounded, name, args)
		} else {
			value, err = s.call(bounded, name, args)
		}
		if err != nil {
			if (route || network || profile || cf || setting) && !readonly {
				return writeFailure(err, args), nil
			}
			return failure(errorCode(err), retryCode(errorCode(err))), nil
		}
		normalized := jsonCopy(value)
		if out.Validate(normalized) != nil {
			if (route || network || profile || cf || setting) && !readonly {
				return writeFailure(errors.New("internal_error"), args), nil
			}
			return failure("internal_error", "never"), nil
		}
		// Text-only clients (including DSH) need the same redacted fields to
		// perform revision-checked writes and recover operations. Never send raw RPCs.
		result := toolResult(normalized, false)
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil || len(encoded) > wireResponseLimit {
			if (route || network || profile || cf || setting) && !readonly {
				return writeFailure(errors.New("result_too_large"), args), nil
			}
			return failure("result_too_large", "never"), nil
		}
		return result, nil
	})
	return nil
}
