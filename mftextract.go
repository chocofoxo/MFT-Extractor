package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	flag "github.com/spf13/pflag"
)

//Global variables pointing to path for each disk
var path string = `\\.\`

//VBR struct stores key data from VBR and a reader to parse
type vbr struct {
	reader            *bytes.Reader
	bytesPerSector    uint16
	sectorsPerCluster uint16
	mftLogClustNum    uint64
	mftRecordSize     uint32
}

//MFT struct stores key information used across MFT methods and reader
type mft struct {
	reader          *bytes.Reader
	startBytes      uint64
	bytesPerCluster uint64
	selfRecord      []byte
	sizeLogical     uint64
	dataRunsAll     []dataRun
}

//DataRun struct stores key information from each data run
type dataRun struct {
	numBytes    uint64
	offsetBytes int64
}

//MFTLocations stores the starting and ending byte offset for each MFT section as well as the length of that section
type mftLocations struct {
	start uint64
	end   uint64
	bytes uint64
}

func main() {
	//Set up volume flag to allow a different volume to be selected
	defVol := os.Getenv("SystemDrive")
	defDir, _ := os.Getwd()

	volume := flag.StringP("volume", "v", defVol, "Select volume from which to extract MFT.")
	dir := flag.StringP("directory", "d", defDir, "Select directory where to save extracted MFT.")
	flag.Parse()

	letter := strings.Trim(*volume, `:\`)
	//Assign path to volume
	volumePath := path + letter + ":"

	//Check that volume path and selected directory are valid
	if _, errVol := os.Stat(volumePath); os.IsNotExist(errVol) {
		log.Fatalf("Selected volume '%s' does not exist. Please input in the format 'A' or 'A:'\n", *volume)
	}
	if _, errDir := os.Stat(*dir); os.IsNotExist(errDir) {
		log.Fatalf("Selected directory '%s' does not exist.\n", *dir)
	}

	//Parse VBR (includes check that running as admin)
	vbr := vbr{} //Create new VBR struct to store data
	vbr.parseVBR(volumePath)

	mft := mft{} //Create new MFT struct to store data

	//Obtain starting offset of MFT
	mft.bytesPerCluster = uint64(vbr.bytesPerSector * vbr.sectorsPerCluster)
	mft.startBytes = mft.bytesPerCluster * vbr.mftLogClustNum //convert starting value of MFT to bytes

	//set up slice to store MFT Self-Record data
	mft.selfRecord = make([]byte, vbr.mftRecordSize)

	//Parse MFT data runs to obtain information on how to find rest of MFT
	mft.parseMFTDataRuns(volumePath)

	//Extract rest of MFT using data runs and export as .bin
	mft.extractMFT(volumePath, *dir)

}

//MFT Method extractMFT uses parsed data runs to extract sections of the MFT from the disk and combine them into one file, MFT.bin
func (mft *mft) extractMFT(volumePath string, dir string) {

	//Calculate starting and ending byte offset for each data run
	//Starting offset : previous data run start + offset
	//Ending offset: starting offset + length - 1

	mftLocationsAll := []mftLocations{} //initialize an MFTLocations struct
	var offsetVol int64 = 0             //start offset at beginning of volume
	var totalSize uint64 = 0            //track total size of mft
	for _, datarun := range mft.dataRunsAll {
		mftlocation := mftLocations{}
		mftlocation.start = uint64(datarun.offsetBytes + offsetVol)
		mftlocation.bytes = datarun.numBytes
		mftlocation.end = mftlocation.start + mftlocation.bytes - 1
		totalSize += mftlocation.bytes

		//ensure that bytes extracted do not exceed size of MFT
		if totalSize > mft.sizeLogical {
			totalSize -= mftlocation.bytes //return totalSize to previous value
			mftlocation.bytes = mft.sizeLogical - totalSize
			mftlocation.end = mftlocation.start + mftlocation.bytes - 1
			totalSize += mftlocation.bytes
		}
		mftLocationsAll = append(mftLocationsAll, mftlocation)
		offsetVol = int64(mftlocation.start) //move offset to start of data run
	}

	//Get handle on disk volume
	volume, errOpen := os.Open(volumePath)
	if errOpen != nil {
		log.Fatalf("Error while opening volume: %s\n", errOpen)
	}

	//Create MFT.bin file and open to append
	volName := string(volumePath[4]) //Save drive letter
	timestamp := time.Now().UTC().Format("20060102T150405Z-")
	fileName := timestamp + volName + "-MFT.bin"
	filePath := filepath.Join(dir, fileName)

	mftFile, errCreate := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if errCreate != nil {
		log.Fatalf("Error while creating %s: %s\n", fileName, errCreate)
	}

	//Loop through each location of the MFT and write to MFT.bin

	//Update message
	fmt.Print("\t\t    Extracting MFT using data runs...")

	for _, location := range mftLocationsAll {

		//Seek to start of MFT on volume
		if _, errSeek := volume.Seek(int64(location.start), 0); errSeek != nil {
			log.Fatalf("Error occured when seeking MFT data: %s\n", errSeek)
		}

		mftdata := make([]byte, location.bytes) //make temporary byte slice to store data

		//Store MFT data to temporary slice
		if _, errRead := volume.Read(mftdata); errRead != nil {
			log.Fatalf("Error occured when reading MFT data: %s\n", errRead)
		}

		//Write slice to file
		if _, errWrite := mftFile.Write(mftdata); errWrite != nil {
			log.Fatalf("Error occured when writing MFT data: %s\n", errWrite)
		}
		//Update message
		fmt.Print(".")
	}

	//Update message
	fmt.Print("done!\n")

	if mft.sizeLogical == totalSize {
		fmt.Printf("\t\t    Extracted %v bytes, equivalent to logical size of MFT\n", totalSize)
	} else {
		fmt.Printf("\t\t    Extracted %v bytes, less than logical size of MFT (%v bytes)\n", totalSize, mft.sizeLogical)
	}

	//Success message
	log.Printf("MFT from %s: stored in file %s\n", volName, filePath)

	//Close file
	mftFile.Close()

	//Close volume
	volume.Close()
}

//MFT Method parseMFTDataRuns extracts the MFT data runs from the MFT self-record and parses into information about the location of each section of the MFT on disk
func (mft *mft) parseMFTDataRuns(volumePath string) {

	//Get handle on disk volume C
	volume, errOpen := os.Open(volumePath)
	if errOpen != nil {
		log.Fatalf("Error while opening volume: %s\n", errOpen)
	}

	//Save MFT Self-Record into memory and close volume handle
	_, errSeek := volume.Seek(int64(mft.startBytes), 0) //Seek to start of MFT on volume

	if errSeek != nil {
		log.Fatalf("Error occured when seeking MFT: %s\n", errSeek)
	}

	_, errRead := volume.Read(mft.selfRecord) //Store MFT

	if errRead != nil {
		log.Fatalf("Error occured when reading bytes in MFT self-record: %s\n", errRead)
	}

	volume.Close()

	//Create new Reader for MFT Self-Record
	mft.reader = bytes.NewReader(mft.selfRecord)

	//Check that MFT Self-Record is valid (starts with FILE, not BAAD)
	startsigMFT := string(mft.readBytes(0, 4)) //starting signature in string
	if startsigMFT != "FILE" {
		log.Fatalf("Wrong starting signature for MFT Record: %s\n", startsigMFT)
	}

	//Obtain offset to first attribute
	offsetFirstAttr := int64(binary.LittleEndian.Uint16(mft.readBytes(20, 2)))

	//Jump to offset, check ID, determine size of attribute and next offset until reached data attribute
	var attrID byte = 0x00
	var dataAttrID byte = 0x80
	var offsetDataAttr int64 = 0

	attrIDSlice := make([]byte, 1)
	sizeAttr := offsetFirstAttr //to set up for loop and first offset

	for attrID != dataAttrID {
		offsetDataAttr += sizeAttr
		attrIDSlice = mft.readBytes(offsetDataAttr, 1)
		attrID = attrIDSlice[0]
		sizeAttr = int64(binary.LittleEndian.Uint32(mft.readBytes(offsetDataAttr+4, 4)))
	}

	//Parse Data Attribute of MFT Self-Record to obtain size and data runs
	offsetRuns := int64(binary.LittleEndian.Uint16(mft.readBytes(offsetDataAttr+32, 2))) //Offset to Data Runs in int64
	mft.sizeLogical = binary.LittleEndian.Uint64(mft.readBytes(offsetDataAttr+48, 8))    //Logical size of MFT, to use later during extraction

	//Extract Data Runs
	dataRunsRaw := mft.readBytes(offsetRuns+offsetDataAttr, sizeAttr-offsetRuns) //read from start of data runs to end of data attribute

	//Set up slice of DataRun structs to parse data into
	mft.dataRunsAll = make([]dataRun, 0)

	//Create reader for data runs
	dataRunReader := bytes.NewReader(dataRunsRaw)

	//Until the end of the data runs (0), read control byte to read data runs and store in structs in mft.dataRunsAll
	datarun := dataRun{} //initialize single data run to add to mft.dataRunsAll
	ctrlByte, _ := dataRunReader.ReadByte()

	for ctrlByte != 0x00 {

		//Low nibble determines number of next bytes in data run that indicate number of contiguous clusters.
		lenNumClusters := uint64(ctrlByte & 0x0F)

		//High nibble determines number of following bytes in data run that indicate cluster offset to data clusters.
		lenClusterOffset := uint64(ctrlByte & 0xF0 >> 4)

		//Obtain number of clusters and pad with zeroes for uint64 conversion
		numClusters := make([]byte, lenNumClusters)
		dataRunReader.Read(numClusters)

		for len(numClusters) < 8 {
			numClusters = append(numClusters, 0x00)
		}

		//Convert number of contiguous bytes of data run and add to datarun struct
		datarun.numBytes = mft.bytesPerCluster * binary.LittleEndian.Uint64(numClusters)

		//Obtain cluster offset and pad with zeroes for uint64 conversion
		clusterOffset := make([]byte, lenClusterOffset)
		dataRunReader.Read(clusterOffset)

		for len(clusterOffset) < 8 {
			clusterOffset = append(clusterOffset, 0x00)
		}

		//Convert to Uint64 and if negative and perform twos complement with bit shifting
		clusterOffsetInt := int64(binary.LittleEndian.Uint64(clusterOffset))

		if clusterOffset[lenClusterOffset-1] > 0x7F {
			clusterOffsetInt = clusterOffsetInt - (1 << (lenClusterOffset * 8)) //twos complement with bits
		}

		//Store in datarun struct
		datarun.offsetBytes = int64(mft.bytesPerCluster) * clusterOffsetInt

		//append datarun struct to mft.dataRunsAll
		mft.dataRunsAll = append(mft.dataRunsAll, datarun)

		//set next control byte
		ctrlByte, _ = dataRunReader.ReadByte()

	}

	//Success message
	log.Printf("Successfully parsed %v MFT data runs\n", len(mft.dataRunsAll))

}

//MFT Method readBytes takes offset and number of bytes to read and returns slice of bytes
func (mft *mft) readBytes(offset int64, bytes int64) []byte {
	MFTreader := mft.reader //convert to local variable

	//Seek to offset from beginning of data
	_, errSeek := MFTreader.Seek(offset, 0)
	//log errors
	if errSeek != nil {
		log.Fatalf("Error occured when seeking to offset of MFT field: %s\n", errSeek)
	}

	//Extract bytes of interest into slice hexbytes
	hexbytes := make([]byte, bytes)
	_, errHex := MFTreader.Read(hexbytes)
	//log error
	if errHex != nil {
		log.Fatalf("Error occured when reading MFT: %s\n", errHex)
	}

	return hexbytes
}

//VBR Method parseVBR extracts key information about MFT from VBR on volume selected
func (vbr *vbr) parseVBR(volumePath string) {

	//Get handle on disk volume
	volume, errOpen := os.Open(volumePath)
	if errOpen != nil {
		log.Fatalf("Error while opening volume: %s\n", errOpen)
	}

	//Read VBR and store in struct, close handle on volume
	data := make([]byte, 512)
	_, errRead := volume.Read(data)

	//Check that program is being run as admin
	errReadstr := fmt.Sprintf("%s", errRead)
	errAdmin := "read " + volumePath + ": The handle is invalid."
	if errRead != nil {
		if errReadstr == errAdmin {
			log.Fatalf("ERROR: Program must be run with admin privileges.")
		} else {
			log.Fatalf("Error occured when reading bytes in file: %+s\n", errRead)
		}
	}
	volume.Close()

	//Create new Reader for VBR data
	vbr.reader = bytes.NewReader(data)

	//Check that volume is NTFS
	oemName := string(vbr.readBytes(3, 4)) //starting signature in string
	if oemName != "NTFS" {
		log.Fatalf("Volume OEM identifier is %s, needs to be NTFS\n", oemName)
	}

	//Extract key data (listed in VBR struct) to determine key information about MFT
	bytesPerSectorBytes := vbr.readBytes(11, 2)
	vbr.bytesPerSector = binary.LittleEndian.Uint16(bytesPerSectorBytes)

	sectorsPerClusterBytes := vbr.readBytes(13, 2)
	vbr.sectorsPerCluster = binary.LittleEndian.Uint16(sectorsPerClusterBytes)

	mftLogClusNumBytes := vbr.readBytes(48, 8)
	vbr.mftLogClustNum = binary.LittleEndian.Uint64(mftLogClusNumBytes)

	//if greater than 127, 2^(absolute value of negative representation)
	mftRecordSizeBytes := vbr.readBytes(64, 1)
	if mftRecordSizeBytes[0] < 0x80 {
		vbr.mftRecordSize = uint32(mftRecordSizeBytes[0]) * uint32(vbr.bytesPerSector*vbr.sectorsPerCluster) //If displayed in number of clusters, convert to bytes
	} else {
		vbr.mftRecordSize = uint32(math.Exp2(math.Abs(float64(^mftRecordSizeBytes[0] + 1))))
	}

	//Success message
	log.Printf("Successfully parsed VBR. Size of each MFT record is %v bytes\n", vbr.mftRecordSize)

}

//VBR Method readBytes takes offset and number of bytes to read and returns slice of read bytes
func (vbr *vbr) readBytes(offset int64, bytes int) []byte {

	VBRreader := vbr.reader //convert to local variable

	//Seek to offset from beginning of data
	_, errSeek := VBRreader.Seek(offset, 0)
	//log errors
	if errSeek != nil {
		log.Fatalf("Error occured when seeking to offset of VBR field: %s\n", errSeek)
	}

	//Extract bytes of interest into slice retBytes
	retBytes := make([]byte, bytes)

	_, errHex := VBRreader.Read(retBytes)
	//log error
	if errHex != nil {
		log.Fatalf("Error occured when reading VBR: %s\n", errHex)
	}

	return retBytes
}
