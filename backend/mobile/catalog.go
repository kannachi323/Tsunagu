package mobile

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/vektah/gqlparser/v2/ast"
	"tsunagu/backend/internal/api/graph"
)

// catalogHandler mounts the existing Tsunagu schema/resolvers, restricted to the
// services initialized on mobile. Reject the whole operation before side effects;
// aliases and fragments cannot bypass the root-field capability check.
func catalogHandler(resolver *graph.Resolver) http.Handler {
	q, sc := resolver.Q, resolver.Sc
	executable := graph.NewExecutableSchema(graph.Config{Resolvers: resolver})
	srv := handler.NewDefaultServer(executable)
	srv.Use(extension.FixedComplexityLimit(1000))
	srv.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		op := graphql.GetOperationContext(ctx).Operation
		allowed := map[string]bool{}
		schema := executable.Schema()
		root := schema.Query
		if op.Operation == ast.Mutation {
			root = schema.Mutation
		}
		for _, field := range root.Fields {
			allowed[field.Name] = true
		}
		for _, name := range []string{"cloudflareSolver", "installCloudflareSolver", "uninstallCloudflareSolver", "setPassword", "disableServerAuth"} {
			delete(allowed, name)
		}
		if !sc.Ready() {
			for _, name := range []string{"resolveMedia", "search", "filterOptions", "sourcePreferences", "popularManga", "latestUpdates", "installExtension", "installExternalExtension", "uninstallExtension", "updateExtension", "setSourcePreference", "refreshMetadata", "syncChapters", "startLibraryUpdate", "refreshFolder", "migrateMedia", "enqueueDownload", "retryDownload", "startDownloader"} {
				delete(allowed, name)
			}
		}
		if err := validateMobileFields(op.SelectionSet, graphql.GetOperationContext(ctx).Variables, resolver.Cfg.Config().DataDir); err != nil {
			return func(context.Context) *graphql.Response { return graphql.ErrorResponse(ctx, "%s", err) }
		}
		if !allowedRootFields(op.SelectionSet, allowed) {
			return func(context.Context) *graphql.Response {
				return graphql.ErrorResponse(ctx, "operation requires a backend capability not yet available on iOS")
			}
		}
		return next(ctx)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		srv.ServeHTTP(w, r.WithContext(graph.WithLoaders(r.Context(), q)))
	})
}

func allowedRootFields(set ast.SelectionSet, allowed map[string]bool) bool {
	for _, selection := range set {
		switch s := selection.(type) {
		case *ast.Field:
			if !allowed[s.Name] {
				return false
			}
		case *ast.InlineFragment:
			if !allowedRootFields(s.SelectionSet, allowed) {
				return false
			}
		case *ast.FragmentSpread:
			if s.Definition == nil || !allowedRootFields(s.Definition.SelectionSet, allowed) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validateMobileFields(set ast.SelectionSet, variables map[string]interface{}, directory string) error {
	for _, selection := range set {
		switch field := selection.(type) {
		case *ast.Field:
			args := field.ArgumentMap(variables)
			if field.Name == "updateServerSetting" {
				key, _ := args["key"].(string)
				switch key {
				case "content_filter_level", "manga_download_format", "tracker_poll_hours", "backup_interval_hours", "backup_retention_count":
				default:
					return fmt.Errorf("setting %s cannot be changed while embedded services are running", key)
				}
			}
			if field.Name == "relocateDownloads" || field.Name == "relocateLocalSource" {
				path, _ := args["newPath"].(string)
				canonical, err := filepath.EvalSymlinks(path)
				if err != nil {
					return fmt.Errorf("relocation requires an existing app-owned directory")
				}
				root, err := filepath.EvalSymlinks(directory)
				if err != nil {
					return err
				}
				relative, err := filepath.Rel(root, canonical)
				if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return fmt.Errorf("relocation must remain inside app-owned storage")
				}
			}
		case *ast.InlineFragment:
			if err := validateMobileFields(field.SelectionSet, variables, directory); err != nil {
				return err
			}
		case *ast.FragmentSpread:
			if field.Definition != nil {
				if err := validateMobileFields(field.Definition.SelectionSet, variables, directory); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
