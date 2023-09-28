// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package autocomplete

import (
	"log/slog"
	"regexp"

	"github.com/microsoft/clac/autocomplete/model"
	"github.com/microsoft/clac/autocomplete/specs"
)

var (
	cmdDelimiter       = regexp.MustCompile(`(\|\|)|(&&)|(;)`)
	lastSuggestionCmd  = ""
	lastSuggestion     = []Suggestion{}
	lastArgDescription = ""
	lastCmdRunes       = 0
)

type Suggestion struct {
	Name        string
	NamePrefix  string
	Description string
}

func getOption(token string, options []model.Option) *model.Option {
	for _, option := range options {
		for _, optionName := range option.Name {
			if token == optionName {
				return &option
			}
		}
	}
	return nil
}

func getSubcommand(token string, spec model.Subcommand) *model.Subcommand {
	for _, subcommand := range spec.Subcommands {
		for _, subcommandName := range subcommand.Name {
			if token == subcommandName {
				return &subcommand
			}
		}
	}
	return nil
}

func argsAreOptional(args []model.Arg) bool {
	allOptional := true
	for _, arg := range args {
		allOptional = allOptional && arg.IsOptional
	}
	return allOptional
}

func getLongName(names []string) string {
	longestName := ""
	for _, name := range names {
		if len(name) > len(longestName) {
			longestName = name
		}
	}
	return longestName
}

func getShortName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	shortestName := names[0]
	for _, name := range names {
		if len(name) < len(shortestName) {
			shortestName = name
		}
	}
	return shortestName
}

func getPersistentTokens(tokens []model.ProcessedToken) []model.ProcessedToken {
	persistentTokens := []model.ProcessedToken{}
	for _, token := range tokens {
		if token.Persist {
			persistentTokens = append(persistentTokens, token)
		}
	}
	return persistentTokens
}

func getPersistentOptions(existingOptions []model.Option, newOptions []model.Option) []model.Option {
	exists := make(map[string]struct{})
	for _, existingOption := range existingOptions {
		for _, key := range existingOption.Name {
			exists[key] = struct{}{}
		}
	}
	for _, newOption := range newOptions {
		hasNewKeys := false
		for _, key := range newOption.Name {
			if _, ok := exists[key]; !ok {
				hasNewKeys = true
				break
			}
		}
		if newOption.IsPersistent && hasNewKeys {
			existingOptions = append(existingOptions, newOption)
		}
	}
	return existingOptions
}

func getSubcommandDrivenRecommendation(spec model.Subcommand, persistentOptions []model.Option, partialCmd *commandToken, argsDepleted bool, argsFromSubcommand bool, acceptedTokens []model.ProcessedToken) model.TermSuggestions {
	suggestions := []model.TermSuggestion{}
	allOptions := append(spec.Options, persistentOptions...)
	var partialToken *string = nil
	if partialCmd != nil {
		partialToken = &partialCmd.token
	}

	if argsDepleted && argsFromSubcommand {
		return model.TermSuggestions{
			Suggestions: suggestions,
		}
	}
	if len(spec.Args) != 0 {
		activeArg := spec.Args[0]
		getGeneratorDrivenRecommendations(activeArg.Generator, &suggestions, partialToken, spec.FilterStrategy, acceptedTokens)
		getSuggestionDrivenRecommendations(activeArg.Suggestions, &suggestions, partialToken, spec.FilterStrategy)
		getTemplateDrivenRecommendations(activeArg.Templates, &suggestions, partialToken, spec.FilterStrategy)
	}
	if !argsFromSubcommand {
		getSubcommandDrivenRecommendations(spec, &suggestions, partialToken, spec.FilterStrategy)
		getOptionDrivenRecommendations(allOptions, &suggestions, acceptedTokens, partialToken, spec.FilterStrategy)
	}
	removeDuplicateRecommendation(&suggestions, acceptedTokens)

	return model.TermSuggestions{
		Suggestions: suggestions,
	}
}

func getArgDrivenRecommendation(args []model.Arg, spec model.Subcommand, persistentOptions []model.Option, partialCmd *commandToken, acceptedTokens []model.ProcessedToken, variadicArgIsBound bool) model.TermSuggestions {
	suggestions := []model.TermSuggestion{}
	activeArg := args[0]
	allOptions := append(spec.Options, persistentOptions...)
	var partialToken *string = nil
	if partialCmd != nil {
		partialToken = &partialCmd.token
	}

	getGeneratorDrivenRecommendations(activeArg.Generator, &suggestions, partialToken, activeArg.FilterStrategy, acceptedTokens)
	getSuggestionDrivenRecommendations(activeArg.Suggestions, &suggestions, partialToken, activeArg.FilterStrategy)
	getTemplateDrivenRecommendations(activeArg.Templates, &suggestions, partialToken, activeArg.FilterStrategy)

	if (activeArg.IsOptional && !activeArg.IsVariadic) || (activeArg.IsVariadic && activeArg.IsOptional && !variadicArgIsBound) {
		getSubcommandDrivenRecommendations(spec, &suggestions, partialToken, activeArg.FilterStrategy)
		getOptionDrivenRecommendations(allOptions, &suggestions, acceptedTokens, partialToken, activeArg.FilterStrategy)
	}
	removeDuplicateRecommendation(&suggestions, acceptedTokens)

	argDescriptionSuggestion := ""
	if len(suggestions) == 0 {
		argDescriptionSuggestion = activeArg.Name
	}

	return model.TermSuggestions{
		ArgumentDescription: argDescriptionSuggestion,
		Suggestions:         suggestions,
	}
}

func handleSubcommand(tokens []commandToken, spec model.Subcommand, persistentOptions []model.Option, argsDepleted, argsUsed bool, acceptedTokens []model.ProcessedToken) (suggestions model.TermSuggestions) {
	if len(tokens) == 0 {
		return getSubcommandDrivenRecommendation(spec, persistentOptions, nil, argsDepleted, argsUsed, acceptedTokens)
	} else if !tokens[0].complete {
		return getSubcommandDrivenRecommendation(spec, persistentOptions, &tokens[0], argsDepleted, argsUsed, acceptedTokens)
	}

	persistentOptions = getPersistentOptions(persistentOptions, spec.Options)
	activeCmd := tokens[0]
	if activeCmd.isOption {
		if option := getOption(activeCmd.token, append(spec.Options, persistentOptions...)); option != nil {
			return handleOption(tokens, *option, spec, persistentOptions, acceptedTokens)
		}
		return
	}
	if subcommand := getSubcommand(activeCmd.token, spec); subcommand != nil {
		return handleSubcommand(tokens[1:], *subcommand, persistentOptions, false, false, getPersistentTokens(acceptedTokens))
	}

	if len(spec.Args) == 0 { // not subcommand or option & no args exist
		return
	}

	return handleArg(tokens, spec.Args, spec, persistentOptions, acceptedTokens, false, false)
}

func handleOption(tokens []commandToken, option model.Option, spec model.Subcommand, persistentOptions []model.Option, acceptedTokens []model.ProcessedToken) (suggestions model.TermSuggestions) {
	if len(tokens) == 0 {
		slog.Error("invalid state reached, option with no tokens")
		return
	}
	activeOption := tokens[0]
	isPersistent := false
	for _, persistentOption := range persistentOptions {
		for _, persistentOptionName := range persistentOption.Name {
			if activeOption.token == persistentOptionName {
				isPersistent = true
				goto persistenceDetermined
			}
		}
	}
persistenceDetermined:
	acceptedTokens = append(acceptedTokens, model.ProcessedToken{Token: activeOption.token, Persist: isPersistent})
	if len(option.Args) == 0 {
		return handleSubcommand(tokens[1:], spec, persistentOptions, false, false, acceptedTokens)
	}
	return handleArg(tokens[1:], option.Args, spec, persistentOptions, acceptedTokens, true, false)
}

func handleArg(tokens []commandToken, args []model.Arg, spec model.Subcommand, persistentOptions []model.Option, acceptedTokens []model.ProcessedToken, fromOption, fromVariadic bool) (suggestions model.TermSuggestions) {
	if len(args) == 0 {
		return handleSubcommand(tokens, spec, persistentOptions, true, !fromOption, acceptedTokens)
	} else if len(tokens) == 0 {
		return getArgDrivenRecommendation(args, spec, persistentOptions, nil, acceptedTokens, fromVariadic)
	} else if !tokens[0].complete {
		return getArgDrivenRecommendation(args, spec, persistentOptions, &tokens[0], acceptedTokens, fromVariadic)
	}

	activeCmd := tokens[0]
	if argsAreOptional(args) {
		if activeCmd.isOption {
			if option := getOption(activeCmd.token, append(spec.Options, persistentOptions...)); option != nil {
				return handleOption(tokens, *option, spec, persistentOptions, acceptedTokens)
			}
			return
		}
		subcommand := getSubcommand(activeCmd.token, spec)
		if subcommand != nil {
			return handleSubcommand(tokens[1:], *subcommand, persistentOptions, false, false, getPersistentTokens(acceptedTokens))
		}
	}

	activeArg := args[0]
	acceptedTokens = append(acceptedTokens, model.ProcessedToken{Token: activeCmd.token, Persist: false})
	if activeArg.IsVariadic {
		return handleArg(tokens[1:], args, spec, persistentOptions, acceptedTokens, fromOption, true)
	} else if activeArg.IsCommand {
		if len(tokens) <= 1 {
			return
		}
		activeCmd = tokens[1]
		if subcommand := getSubcommand(activeCmd.token, spec); subcommand != nil {
			return handleSubcommand(tokens[2:], *subcommand, persistentOptions, false, false, []model.ProcessedToken{})
		}
		return
	}
	return handleArg(tokens[1:], args[1:], spec, persistentOptions, acceptedTokens, fromOption, false)
}

func loadSuggestions(cmd string) (suggestions model.TermSuggestions, charsInLastCmd int) {
	activeCmd := ParseCommand(cmd)
	if len(activeCmd) <= 0 {
		return
	}
	rootToken := activeCmd[0]
	if !rootToken.complete {
		return
	}
	lastCmd := activeCmd[len(activeCmd)-1]
	charsInLastCmd = len(lastCmd.token)
	if lastCmd.complete {
		charsInLastCmd = 0
	}
	if spec, ok := specs.Specs[rootToken.token]; ok {
		return handleSubcommand(activeCmd[1:], spec, []model.Option{}, false, false, []model.ProcessedToken{}), charsInLastCmd
	}
	return
}

func LoadSuggestions(cmd string) ([]Suggestion, string, int) {
	if cmd == lastSuggestionCmd {
		return lastSuggestion, lastArgDescription, lastCmdRunes
	}
	termSuggestions, lastRunes := loadSuggestions(cmd)

	suggestions := []Suggestion{}
	for _, suggestion := range termSuggestions.Suggestions {
		icon := model.TermIcons[suggestion.Type]
		suggestions = append(suggestions, Suggestion{Name: suggestion.Name, NamePrefix: icon, Description: suggestion.Description})
	}
	lastSuggestionCmd, lastSuggestion, lastCmdRunes, lastArgDescription = cmd, suggestions, lastRunes, termSuggestions.ArgumentDescription
	return suggestions, termSuggestions.ArgumentDescription, lastRunes
}
