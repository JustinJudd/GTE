package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	flag "github.com/spf13/pflag"
	"gopkg.in/yaml.v2"
)

const introspectionQuery = `query IntrospectionQuery {
    __schema {
      queryType { name }
      mutationType { name }
      subscriptionType { name }
      types {
        ...FullType
      }
      directives {
        name
        description
        args {
          ...InputValue
        }
      }
    }
  }
  fragment FullType on __Type {
    kind
    name
    description
    fields(includeDeprecated: true) {
      name
      description
      args {
        ...InputValue
      }
      type {
        ...TypeRef
      }
      isDeprecated
      deprecationReason
    }
    inputFields {
      ...InputValue
    }
    interfaces {
      ...TypeRef
    }
    enumValues(includeDeprecated: true) {
      name
      description
      isDeprecated
      deprecationReason
    }
    possibleTypes {
      ...TypeRef
    }
  }
  fragment InputValue on __InputValue {
    name
    description
    type { ...TypeRef }
    defaultValue
  }
  fragment TypeRef on __Type {
    kind
    name
    ofType {
      kind
      name
      ofType {
        kind
        name
        ofType {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
              ofType {
                kind
                name
                ofType {
                  kind
                  name
                }
              }
            }
          }
        }
      }
    }
  }`

type server struct {
	template *template.Template
	schema   string
}

type MessageType int

const (
	Info MessageType = iota
	Primary
	Success
	Warning
	Error
)

type Message struct {
	Text string
	Type MessageType
}

var portFlag int
var remoteFlag string
var listenFlag string

func init() {
	flag.IntVarP(&portFlag, "port", "p", 9090, "port to run GTE web interface on")
	flag.StringVarP(&listenFlag, "listen", "l", "localhost", "address for GTE to listen on")
	flag.StringVarP(&remoteFlag, "remote", "r", "https://swapi.graph.cool/", "remote server to proxy GraphQL requests to")
}

func main() {

	flag.Parse()

	s := server{}

	s.template = template.Must(template.New("").ParseGlob(filepath.Join("templates", "*.tmpl")))

	query := fmt.Sprintf(`{"query":"%s"}`, strings.Replace(introspectionQuery, "\n", "\\n", -1))
	buf := bytes.NewBufferString(query)
	resp, err := http.Post(remoteFlag, "application/json", buf)
	if err != nil {
		fmt.Println("Unable to get schema from GraphQL Endpoint")
		fmt.Println(err)
		return
	}
	d, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Unable to get schema from GraphQL Endpoint")
		fmt.Println(err)
		return
	}
	s.schema = string(d)

	// Serves up the main web page
	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		query = ""
		defer func() {
			if err != nil {
				msg := Message{Text: err.Error(), Type: Error}
				s.template.ExecuteTemplate(w, "root", map[string]interface{}{"query": query, "tableData": nil, "vars": nil, "message": msg, "schema": s.schema, "dataSet": "", "remote": remoteFlag})
			}
			return
		}()
		r.ParseForm()
		query := r.PostFormValue("query")
		query, err = EnhanceQuery(r)
		if err != nil {
			return
		}
		if len(query) == 0 {
			err = fmt.Errorf("No query provided")
			return
		}
		q := strings.Fields(query)
		query = strings.Join(q, " ")
		finishedQuery := fmt.Sprintf(`{"query":"%s"}`, query)

		tableData, vars := ExtractTableInfo(r)

		if len(vars) == 0 {
			vars = []string{"data"}
			tableData = map[string]string{
				"data": "Data...",
			}
		}

		buf := bytes.NewBufferString(finishedQuery)
		responseJSON, err := postQuery(buf)

		type data struct {
			Data json.RawMessage
		}
		d := data{}
		json.Unmarshal([]byte(responseJSON), &d)

		if err != nil {
			return
		}

		s.template.ExecuteTemplate(w, "root", map[string]interface{}{"query": query, "tableData": tableData, "vars": vars, "schema": s.schema, "dataSet": template.JS(d.Data), "remote": remoteFlag})

	}))

	// Maine graphql query endpoint. Proxies requests and normalizes responses
	http.Handle("/queryNormalized", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responseJSON, err := postQuery(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Write([]byte(responseJSON))
	}))

	// Serves static content for the app
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", listenFlag, portFlag), nil))

}

func postQuery(body io.Reader) (string, error) {
	resp, err := http.Post(remoteFlag, "application/json", body)
	defer resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("Error forwarding query: %s", err)
	}

	response := struct {
		Data   json.RawMessage
		Errors []struct {
			Message string
		}
	}{}

	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return "", fmt.Errorf("Error decoding json: %s", err)
	}
	if len(response.Errors) > 0 {
		msg := []string{}
		for _, e := range response.Errors {
			msg = append(msg, e.Message)
		}
		return "", fmt.Errorf(strings.Join(msg, "\n"))
	}

	responseJSON := NormalizeJSON(response.Data)
	return responseJSON, nil
}

// ExtractTableInfo takes the Table Column information to set up Table headers and columns
func ExtractTableInfo(r *http.Request) (tableData map[string]string, vars []string) {
	r.ParseForm()
	tableInfo := r.PostFormValue("table-info")
	m := yaml.MapSlice{}
	err := yaml.Unmarshal([]byte(tableInfo), &m)
	if err != nil {
		fmt.Println(err)
		return
	}

	return recurseTableInfo(m, "")

}

// rcurseTableInfo recursively crawls down the YAML table column info
func recurseTableInfo(m yaml.MapSlice, path string) (tableData map[string]string, vars []string) {

	tableData = map[string]string{}
	for _, item := range m {
		k, v := item.Key, item.Value
		var newPath string
		if len(path) > 0 {
			newPath = fmt.Sprintf("%s-%s", path, k)
		} else {
			newPath = fmt.Sprint(k)
		}

		switch val := v.(type) {
		case string, int, float64:
			tableData[newPath] = fmt.Sprint(val)
			vars = append(vars, fmt.Sprint(newPath))
		case yaml.MapSlice:
			t, subVars := recurseTableInfo(val, newPath)
			vars = append(vars, subVars...)
			for k, v := range t {
				tableData[k] = v
			}
		default: // Type is a map
			fmt.Printf("%T\n", val)

		}
	}
	return
}

// EnhanceQuery takes an incoming http Request and extracts the initial graphql query and selected values from table and injects them into the query
func EnhanceQuery(r *http.Request) (string, error) {
	err := r.ParseForm()
	if err != nil {
		return "", fmt.Errorf("Error getting values to enhance query: %s", err)
	}
	query := r.PostFormValue("query")

	variableCount := strings.Count(query, "$")
	if variableCount > 0 {
		variables := map[string][]string{}
		valuesData := r.PostFormValue("values")
		values := []map[string]interface{}{}
		err = json.Unmarshal([]byte(valuesData), &values)
		if err != nil {
			return "", fmt.Errorf("Error unmarshaling values: %s", err)
		}
		if len(values) == 0 { // No values passed on - maybe no selected rows
			return "", fmt.Errorf("No values provided for variables")
		}
		for _, s := range strings.Split(query, "$")[1:] {
			f := strings.FieldsFunc(s, func(r rune) bool {
				return !unicode.IsLetter(r) && r != '.' && r != '-' && r != '_'
			})
			variables[f[0]] = []string{}
		}
		for _, value := range values {
			checked := false
			for key, val := range value {
				if a, ok := variables[key]; ok {
					checked = true
					switch v := val.(type) {
					case string:
						variables[key] = append(a, strconv.Quote(v))
					case int:
						variables[key] = append(a, strconv.Itoa(v))
					case float64:
						if math.Floor(v) == v {
							variables[key] = append(a, fmt.Sprintf("%.0f", v))
						} else {
							variables[key] = append(a, fmt.Sprintf("%f", v))
						}

					default:
						fmt.Printf("Didn't catch type %T of %v", v, v)
					}
					continue
				}
			}
			if !checked {
				return "", fmt.Errorf("No variable information found for %s", value)
			}
		}

		for variable, val := range variables {
			if len(val) == 0 {
				return "", fmt.Errorf("No value information found for %s", variable)
			}
			var v string
			if len(val) > 1 {
				v = fmt.Sprintf("[%s]", strings.Join(val, ","))
			} else {
				v = val[0]
			}

			query = strings.Replace(query, "$"+variable, v, -1)
		}

	}
	query = strings.Replace(query, "\"", "\\\"", -1)
	return query, nil
}

// NormalizeJSON flattens JSON responses into rows, so that they can be properly displayed in DataTables
/* Normalizing logic -
1. Find out type of level we are working on, string, map, or list
2. If list, split up, call recursive normalize for each item in list, passing what information applies so far
3. If map, iterate through each value calling recursive normalize function
4. Else should be a native value, add to value mapping finish, and return what we have

*/
func NormalizeJSON(m json.RawMessage) string {
	d := normalizeRecurse(m, []string{})

	data := map[string]interface{}{
		"data": d,
	}
	v, _ := json.Marshal(data)
	return string(v)
}

// normalizeRecurse checks an incoming message and tries to unmarshal it to a few common types. Will recursively normalize child objects
func normalizeRecurse(msg json.RawMessage, path []string) []map[string]json.RawMessage {
	localData := map[string]json.RawMessage{}
	m := map[string]json.RawMessage{}
	b, _ := msg.MarshalJSON()
	err := json.Unmarshal(b, &m)
	if err == nil { //msg is a map
		needRecursion := map[string]json.RawMessage{}
		for k, v := range m {
			t, _ := getChildType(v)
			if t == Native {
				localData[fmt.Sprintf("%s-%s", strings.Join(path, "-"), k)] = v
			} else {
				needRecursion[k] = v
			}

		}
		if len(needRecursion) == 0 {
			return []map[string]json.RawMessage{localData}
		}
		singleReturns := []map[string]json.RawMessage{}
		data := []map[string]json.RawMessage{}
		for k, v := range needRecursion {
			d := normalizeRecurse(v, append(path, k))
			if len(d) == 1 {
				for k2, v2 := range localData {
					d[0][k2] = v2

				}
				singleReturns = append(singleReturns, d...)
				continue
			}
			for _, l := range d {
				for k2, v2 := range localData {
					l[k2] = v2

				}
				data = append(data, l)

			}
		}
		if len(data) > 0 {
			for _, m := range data {
				for _, d := range singleReturns {
					for k, v := range d {
						m[k] = v
					}
				}
			}
		} else {
			data = singleReturns
		}

		return data
	}
	l := []json.RawMessage{}
	err = json.Unmarshal(b, &l)
	if err == nil { //msg is a list
		l2 := []map[string]json.RawMessage{}
		for _, v := range l {
			d := normalizeRecurse(v, path)

			l2 = append(l2, d...)
		}
		return l2
	}
	return nil
}

type childType int

const (
	Map childType = iota
	List
	Native
)

// getChildType determines if a json structure is a map, list, or native type
func getChildType(msg json.RawMessage) (childType, interface{}) {
	b, _ := msg.MarshalJSON()

	m := map[string]json.RawMessage{}
	err := json.Unmarshal(b, &m)
	if err == nil {
		return Map, m
	}
	l := []json.RawMessage{}
	err = json.Unmarshal(b, &l)
	if err == nil {
		return List, l
	}

	return Native, msg
}
