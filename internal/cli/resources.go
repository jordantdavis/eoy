package cli

// Resources returns the resources of eoy. Add each new resource here. A
// resource with a "get" command makes "eoy get" appear in help.
func Resources() []Resource {
	return []Resource{
		evidenceResource(),
		pullRequestResource(),
		noteResource(),
		tagResource(),
		mannequinResource(),
	}
}
