package scanner

// SendAnalysisData sends analysis data if the user opts in
func (scanner *Scanner) SendAnalysisData(data map[string]interface{}) {
	scanner.analysisDataSender(data)
}
