package main

import (
	"encoding/json"
	"flag"
	"os"
	"path"
	"strings"

	"github.com/google/osv/vulnfeeds/cves"
	"github.com/google/osv/vulnfeeds/utility"
	"github.com/google/osv/vulnfeeds/vulns"
)

const (
	defaultCvePath        = "cve_jsons"
	defaultPartsInputPath = "parts"
	defaultOSVOutputPath  = "osv_output"
)

var Logger utility.LoggerWrapper

func main() {
	var logCleanup func()
	Logger, logCleanup = utility.CreateLoggerWrapper("combine-to-osv")
	defer logCleanup()

	cvePath := flag.String("cvePath", defaultCvePath, "Path to CVE file")
	partsInputPath := flag.String("partsPath", defaultPartsInputPath, "Path to CVE file")
	osvOutputPath := flag.String("osvOutputPath", defaultOSVOutputPath, "Path to CVE file")
	flag.Parse()

	err := os.MkdirAll(*cvePath, 0755)
	if err != nil {
		Logger.Fatalf("Can't create output path: %s", err)
	}
	err = os.MkdirAll(*osvOutputPath, 0755)
	if err != nil {
		Logger.Fatalf("Can't create output path: %s", err)
	}

	allCves := loadAllCVEs(*cvePath)
	allParts := loadParts(*partsInputPath)
	combinedData := combineIntoOSV(allCves, allParts)
	writeOSVFile(combinedData, *osvOutputPath)
}

// loadInnerParts loads second level folder for the loadParts function
func loadInnerParts(innerPartInputPath string, output map[string][]vulns.PackageInfo) {
	dirInner, err := os.ReadDir(innerPartInputPath)
	if err != nil {
		Logger.Fatalf("Failed to read dir? %s", err)
	}
	for _, entryInner := range dirInner {
		if !strings.HasSuffix(entryInner.Name(), ".json") {
			continue
		}
		file, err := os.Open(path.Join(innerPartInputPath, entryInner.Name()))
		if err != nil {
			Logger.Fatalf("Failed to open PackageInfo JSON: %s", err)
		}
		defer file.Close()
		var pkgInfos []vulns.PackageInfo
		err = json.NewDecoder(file).Decode(&pkgInfos)
		if err != nil {
			Logger.Fatalf("Failed to decode %q: %s", file.Name(), err)
		}

		// Turns CVE-2022-12345.alpine.json into CVE-2022-12345
		cveId := strings.Split(entryInner.Name(), ".")[0]
		output[cveId] = append(output[cveId], pkgInfos...)

		Logger.Infof(
			"Loaded Item: %s", entryInner.Name())
	}
}

// loadParts loads files generated by other executables in the cmd folder.
//
// Expects directory structure of:
//
// - <partsInputPath>/
//   - alpineParts/
//   - CVE-2020-1234.alpine.json
//   - ...
//   - debianParts/
//   - ...
//
// ## Returns
// A mapping of "CVE-ID": []<Affected Package Information>
func loadParts(partsInputPath string) map[string][]vulns.PackageInfo {
	dir, err := os.ReadDir(partsInputPath)
	if err != nil {
		Logger.Fatalf("Failed to read dir? %s", err)
	}
	output := map[string][]vulns.PackageInfo{}
	for _, entry := range dir {
		if !entry.IsDir() {
			Logger.Warnf("Unexpected file entry in " + partsInputPath)
			continue
		}
		// map is already a reference type, so no need to pass in a pointer
		loadInnerParts(path.Join(partsInputPath, entry.Name()), output)
	}
	return output
}

// combineIntoOSV creates OSV entry by combining loaded CVEs from NVD and PackageInfo information from security advisories.
func combineIntoOSV(loadedCves map[string]cves.CVEItem, allParts map[string][]vulns.PackageInfo) map[string]*vulns.Vulnerability {
	Logger.Infof("Begin writing OSV files")
	convertedCves := map[string]*vulns.Vulnerability{}
	for cveId, cve := range loadedCves {
		if len(allParts[cveId]) == 0 {
			continue
		}
		convertedCve, _ := vulns.FromCVE(cveId, cve)
		// Best-effort attempt to mark a disputed CVE as withdrawn.
		modified, err := vulns.CVEIsDisputed(convertedCve)
		if err != nil {
			Logger.Warnf("Unable to determine CVE dispute status of %s: %v", convertedCve.ID, err)
		}
		if err == nil && modified != "" {
			convertedCve.Withdrawn = modified
		}
		for _, pkgInfo := range allParts[cveId] {
			convertedCve.AddPkgInfo(pkgInfo)
		}
		convertedCves[cveId] = convertedCve
	}
	return convertedCves
}

// writeOSVFile writes out the given osv objects into individual json files
func writeOSVFile(osvData map[string]*vulns.Vulnerability, osvOutputPath string) {
	for vId, osv := range osvData {
		file, err := os.OpenFile(path.Join(osvOutputPath, vId+".json"), os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			Logger.Fatalf("Failed to create/open file to write: %s", err)
		}
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(osv)
		if err != nil {
			Logger.Fatalf("Failed to encode OSVs")
		}
		file.Close()
	}

	Logger.Infof("Successfully written all OSV files")
}

// loadAllCVEs loads the downloaded CVE's from the NVD database into memory.
func loadAllCVEs(cvePath string) map[string]cves.CVEItem {
	dir, err := os.ReadDir(cvePath)
	if err != nil {
		Logger.Fatalf("Failed to read dir? %s", err)
	}

	result := make(map[string]cves.CVEItem)

	for _, entry := range dir {
		file, err := os.Open(path.Join(cvePath, entry.Name()))
		if err != nil {
			Logger.Fatalf("Failed to open cve json: %s", err)
		}
		var nvdcve cves.NVDCVE
		err = json.NewDecoder(file).Decode(&nvdcve)
		if err != nil {
			Logger.Fatalf("Failed to decode json: %s", err)
		}

		for _, item := range nvdcve.CVEItems {
			result[item.CVE.CVEDataMeta.ID] = item
		}
		Logger.Infof("Loaded CVE: %s", entry.Name())
		file.Close()
	}
	return result
}
