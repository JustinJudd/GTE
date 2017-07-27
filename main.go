package main

import (
	"bytes"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"unicode"

	"encoding/json"
	"fmt"
	"html/template"

	"strings"

	"gopkg.in/yaml.v2"
)

const (
	address = ":9090"
	//graphQLAddress = "http://127.0.0.1:8080/query"
	//graphQLAddress = "https://www.graphqlhub.com/graphql"
	graphQLAddress = "https://pokeapi-graphiql.herokuapp.com/"
	//graphQLAddress = "http://swapi.graphene-python.org/graphql"
	//graphQLAddress = "https://hackerone.com/graphql"
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

func main() {

	s := server{}

	s.template = template.Must(template.New("").ParseGlob(filepath.Join("templates", "*.tmpl")))

	query := fmt.Sprintf(`{"query":"%s"}`, strings.Replace(introspectionQuery, "\n", "\\n", -1))
	buf := bytes.NewBufferString(query)
	resp, err := http.Post(graphQLAddress, "application/json", buf)
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
				s.template.ExecuteTemplate(w, "root", map[string]interface{}{"query": query, "tableData": nil, "vars": nil, "message": msg, "schema": s.schema})
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
			//query = "{  host {    name    ip    info {      os      platform      uptime    }  }}"
		}
		q := strings.Fields(query)
		query = strings.Join(q, " ")
		query = fmt.Sprintf(`{"query":"%s"}`, query) //  '{"query":"{  host {    name    ip    info {      os      platform      uptime    }  }}"}'

		tableData, vars := ExtractTableInfo(r)

		if len(vars) == 0 {
			vars = []string{"data"}
			tableData = map[string]string{
				"data": "Data...",
			}
		}

		s.template.ExecuteTemplate(w, "root", map[string]interface{}{"query": query, "tableData": tableData, "vars": vars, "schema": s.schema})

	}))

	// Maine graphql query endpoint. Proxies requests and normalizes responses
	http.Handle("/queryNormalized", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := http.Post(graphQLAddress, "application/json", r.Body)
		defer resp.Body.Close()
		if err != nil {
			fmt.Println("Error forwarding query", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		response := struct {
			Data   json.RawMessage
			Errors []struct {
				Message string
			}
		}{}
		err = json.NewDecoder(resp.Body).Decode(&response)
		if err != nil {
			fmt.Println("Error decoding json", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(response.Errors) > 0 {
			http.Error(w, response.Errors[0].Message, http.StatusBadRequest)
			return
		}

		responseJSON := NormalizeJSON(response.Data)

		w.Write([]byte(responseJSON))
	}))

	// Serves static content for the app
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	log.Fatal(http.ListenAndServe(address, nil))

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
		json.Unmarshal([]byte(valuesData), &values)
		if len(values) == 0 { // No values passed on - maybe no selected rows
			return "", fmt.Errorf("No values provided for variables")
		}
		for _, s := range strings.Split(query, "$")[1:] {
			f := strings.FieldsFunc(s, func(r rune) bool {
				return !unicode.IsLetter(r) && r != '.' && r != '-'
			})
			variables[f[0]] = []string{}
		}
		for _, value := range values {
			checked := false
			for key, val := range value {
				if a, ok := variables[key]; ok {
					checked = true
					//fmt.Printf("Key %s was a match\n", key)
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
					break
				}
			}
			if !checked {
				return "", fmt.Errorf("No variable information found for %s", value)
			}
		}

		for variable, val := range variables {
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