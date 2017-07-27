import CodeMirror from 'codemirror';
import 'codemirror/addon/hint/show-hint';
import 'codemirror/addon/lint/lint';
import 'codemirror/addon/edit/matchbrackets';
import 'codemirror/addon/edit/closebrackets';
import 'codemirror/addon/fold/foldgutter';
import 'codemirror/addon/fold/brace-fold';
import 'codemirror/mode/yaml/yaml';
import 'codemirror-graphql/hint';
import 'codemirror-graphql/lint';
import 'codemirror-graphql/mode';
import 'codemirror-graphql/jump';
import 'codemirror-graphql/info';
import 'codemirror/keymap/sublime';
var { graphql, buildSchema, buildClientSchema } = require('graphql');
const { introspectionQuery } = require('graphql');



const AUTO_COMPLETE_AFTER_KEY = /^[a-zA-Z0-9_@(]$/;


function GraphQLEditor(schemaText, element) {
  var schema = buildClientSchema(schemaText);
  var editor =  CodeMirror.fromTextArea(element, {
  mode: 'graphql',
  lint: {
    schema: schema
  },
  hintOptions: {
    schema: schema,
    closeOnUnfocus: false,
    completeSingle: false,
  },
  info: {
    schema: schema,
    renderDescription: text => marked(text, { sanitize: true }),
  },
  jump: {
    schema: schema,
  },
  gutters: [ 'CodeMirror-linenumbers', 'CodeMirror-foldgutter' ],
  keyMap: 'sublime',
  extraKeys: {"Ctrl-Space": "autocomplete"},
  autoCloseBrackets: true,
  matchBrackets: true,
  showCursorWhenSelecting: true,
  foldGutter: {
    minFoldSize: 4
  },
  lineNumbers: true,
  tabSize: 2,
});
//editor.on('keyup', _onKeyUp);
editor.on("keyup", function (cm, event) {
	if (AUTO_COMPLETE_AFTER_KEY.test(event.key)) {
     	 editor.execCommand('autocomplete');
	}
    
});

return editor;
}

function YAMLEditor(element) {
  var editor = CodeMirror.fromTextArea(element, {
  mode: 'yaml',
  keyMap: 'sublime',
  showCursorWhenSelecting: true,
  foldGutter: {
    minFoldSize: 4
  },
  lineNumbers: true,
  tabSize: 2,
  indentUnit: 2,
  indentWithTabs: false,
  extraKeys: {Tab: function(cm)  {
    cm.execCommand("insertSoftTab");
  }}
  
});

return editor;
}


//module.exports = {GraphQLEditor: GraphQLEditor, YAMLEditor: YAMLEditor};

export default {
	GraphQLEditor: GraphQLEditor, 
	YAMLEditor: YAMLEditor
}

