package reference

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/hyperledger/firefly-common/pkg/i18n"
	"github.com/kaleido-io/paladin/toolkit/pkg/tkmsgs"
)

func getIncludeFile(ctx context.Context, outputPath, name string) ([]byte, error) {
	// If a detailed type_description.md file exists, include that in a Description section here
	filename, err := filepath.Abs(filepath.Join(outputPath, "_includes", fmt.Sprintf("%s_description.md", name)))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filename); err != nil {
		return nil, i18n.NewError(ctx, tkmsgs.MsgReferenceMarkdownMissing, filename)
	}
	return []byte(fmt.Sprintf("{%% include-markdown \"./_includes/%s_description.md\" %%}\n\n", name)), nil
}

// shouldFilter checks if a type should be ignored (e.g., context.Context or error).
func shouldFilter(t reflect.Type) bool {
	return t == reflect.TypeOf((*context.Context)(nil)).Elem() ||
		t == reflect.TypeOf((*error)(nil)).Elem()
}

func isEnum(f reflect.Type) bool {
	return f.PkgPath() == "github.com/kaleido-io/paladin/toolkit/pkg/tktypes" && strings.HasPrefix(f.Name(), "Enum[")
}

func generateEnumList(f reflect.Type) string {

	if f.Kind() == reflect.Pointer {
		f = f.Elem()
	}
	optionsMethod, _ := f.MethodByName("Options")
	enumOptions := optionsMethod.Func.Call([]reflect.Value{reflect.New(f).Elem()})[0].Interface().([]string)

	buff := new(strings.Builder)
	for i, v := range enumOptions {
		if i > 0 {
			buff.WriteString(", ")
		}
		buff.WriteString(fmt.Sprintf(`"%s"`, v))
	}
	return buff.String()
}

func getRelativePath(depth int) string {
	path := filepath.Join("doc-site", "docs", "reference")
	for i := 0; i < depth; i++ {
		path = filepath.Join("..", path)
	}
	return path
}
func getType(v interface{}) reflect.Type {
	if reflect.TypeOf(v).Kind() == reflect.Ptr {
		return reflect.TypeOf(v).Elem()
	}
	if reflect.TypeOf(v).Kind() == reflect.Interface {
		return reflect.TypeOf(v).Elem()
	}
	return reflect.TypeOf(v)
}