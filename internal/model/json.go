package model

import "encoding/json"

// MarshalJSON keeps the DecisionGraph API stable for empty repositories.
// Go encodes nil slices as null by default, while dashboard clients expect
// collection fields to always be JSON arrays.
func (graph DecisionGraph) MarshalJSON() ([]byte, error) {
	type decisionGraphJSON DecisionGraph

	if graph.Nodes == nil {
		graph.Nodes = []GraphNode{}
	}
	if graph.Edges == nil {
		graph.Edges = []GraphEdge{}
	}

	return json.Marshal(decisionGraphJSON(graph))
}
