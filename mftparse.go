//MFT Parser
//Input: None, program extract MFT file from native system
//Output: Data parsed from MFT

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os"
)

type VBR struct {
	Reader               *bytes.Reader
	BytesPerSector       []byte
	SectorsPerCluster    []byte
	MFTLogClustNum       []byte
	ClustersPerMFTRecord []byte //if greater than 127, 2^(absolute value of twos complement)
}

func main() {

	//Check that running as admin

	//Get handle on disk volume C
	pathToC := `\\.\C:`
	volC, err := os.Open(pathToC)

	//log errors
	if err != nil {
		log.Fatalf("Error while opening volume: %s\n", err)
	}

	//Create new VBR struct to store data
	vbr := VBR{}

	//Read VBR and store in struct, close handle on volume
	data := readNextBytes(volC, 512)
	fmt.Printf("VBR: %x\n", data)
	volC.Close()

	//Create new Reader for VBR data
	vbr.Reader = bytes.NewReader(data)

	//Extract key data (listed in VBR struct) to determine starting location of MFT on disk
	vbr.BytesPerSector = vbr.parseData(11, 2)
	fmt.Printf("Successfully parsed BytesPerSector: %s\n", vbr.BytesPerSector)

	//Obtain first MFT record
	//Extract entire MFT to current directory
}

func readNextBytes(file *os.File, number int) []byte {
	bytes := make([]byte, number)

	_, err := file.Read(bytes)
	if err != nil {
		log.Fatalf("Error occured when reading bytes in file: %s\n", err)
	}

	return bytes
}

func (vbr *VBR) parseData(offset int64, bytes int) []byte {
	VBRreader := vbr.Reader //convert to variable to look pretty

	_, err1 := VBRreader.Seek(offset, 0) //seek to offset from beginning of data
	//log errors
	if err1 != nil {
		log.Fatalf("Error occured when seeking to offset of VBR field: %s\n", err1)
	}

	//debug: read and print hex bytes

	parsedField := make([]byte, bytes)
	err2 := binary.Read(VBRreader, binary.LittleEndian, &parsedField)
	//log errors
	if err2 != nil {
		log.Fatalf("Error occured when generating parsedField: %s\n", err2)
	}

	return parsedField
}
