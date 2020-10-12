//MFT Parser
//Input: None, program extract MFT file from native system
//Output: Data parsed from MFT

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
)

//VBR struct stores key data from VBR and a Reader to help parse (not exportable)
type VBR struct {
	reader            *bytes.Reader
	BytesPerSector    int
	SectorsPerCluster int
	MFTLogClustNum    int
	MFTRecordSize     int
}

//MFT struct stores reader for mft
type MFT struct {
	reader          *bytes.Reader
	startBytes      int
	bytesPerCluster int
	selfRecord      []byte
	sizeLogical     uint64
	dataRunsAll     []DataRun
	vbr             *VBR
}

//DataRun struct stores key information from each data run
type DataRun struct {
	numBytes    uint64
	offsetBytes int64
}

//MFTLocations stores the starting and ending byte offset for each MFT piece
type MFTLocations struct {
	start int64
	end   int64
	bytes int
}

func main() {
	//Check that running as admin

	//Parse VBR
	vbr := VBR{} //Create new VBR struct to store data
	vbr.parseVBR()

	mft := MFT{}   //Create new MFT struct to store data
	mft.vbr = &vbr //save pointer to corresponding volume

	//Obtain starting offset of MFT
	mft.bytesPerCluster = vbr.BytesPerSector * vbr.SectorsPerCluster
	mft.startBytes = mft.bytesPerCluster * vbr.MFTLogClustNum //convert starting value of MFT to bytes

	//set up slice to store MFT self record data
	mft.selfRecord = make([]byte, vbr.MFTRecordSize)

	//Parse MFT Data Runs to obtain information how to find rest of MFT
	mft.parseMFTDataRuns()

	//Extract rest of MFT using data runs and export as .bin
	mft.extractMFT()
}

func (mft *MFT) extractMFT() {

	//Calculate starting and ending byte offset for each data run
	//Starting offset : previous data run start + offset
	//Ending offset: starting offset + length - 1

	mftLocationsAll := make([]MFTLocations, 0) //set up slice to store locations of MFT data on disk
	var offsetVol int64                        //start offset at beginning of volume
	var totalSize uint64                       //track total size of mft
	for _, datarun := range mft.dataRunsAll {
		mftlocation := MFTLocations{}
		mftlocation.start = datarun.offsetBytes + offsetVol
		mftlocation.end = mftlocation.start + int64(datarun.numBytes-1)
		mftlocation.bytes = int(datarun.numBytes)
		totalSize += uint64(mftlocation.bytes)

		//ensure that bytes extracted do not exceed size of MFT
		if totalSize > mft.sizeLogical {
			break
		}
		mftLocationsAll = append(mftLocationsAll, mftlocation)
		offsetVol = mftlocation.start //move offset to start of data run
	}

	fmt.Printf("MFT Locations: %+v\n", mftLocationsAll)

	//Get handle on disk volume C
	pathToC := `\\.\C:`
	volC, errOpen := os.Open(pathToC)
	if errOpen != nil {
		log.Fatalf("Error while opening volume: %s\n", errOpen)
	}

	//Create/overwrite MFT.bin file and open to append
	if errOW := ioutil.WriteFile("MFT.bin", []byte(""), 0600); errOW != nil {
		log.Fatalf("Error occured when overwriting MFT.bin: %s\n", errOW)
	}

	mftFile, errCreate := os.OpenFile("MFT.bin", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if errCreate != nil {
		log.Fatalf("Error while creating MFT.bin file: %s\n", errCreate)
	}

	//Loop through each location of the MFT and write to MFT.bin

	for _, location := range mftLocationsAll {

		//Seek to start of MFT on volume
		if _, errSeek := volC.Seek(location.start, 0); errSeek != nil {
			log.Fatalf("Error occured when seeking MFT data: %s\n", errSeek)
		}

		mftdata := make([]byte, location.bytes) //make temporary byte slice to store data

		//Store MFT data to temporary slice
		if _, errRead := volC.Read(mftdata); errRead != nil {
			log.Fatalf("Error occured when reading MFT data: %s\n", errRead)
		}

		//Write slice to file
		if _, errWrite := mftFile.Write(mftdata); errWrite != nil {
			log.Fatalf("Error occured when writing MFT data: %s\n", errWrite)
		}
	}
	//Close file
	mftFile.Close()
	//Close volume
	volC.Close()
}
func (mft *MFT) parseMFTDataRuns() {

	//Get handle on disk volume C
	pathToC := `\\.\C:`
	volC, errOpen := os.Open(pathToC)
	if errOpen != nil {
		log.Fatalf("Error while opening volume: %s\n", errOpen)
	}

	//Save MFT Self-Record into memory and close volume handle
	_, errSeek := volC.Seek(int64(mft.startBytes), 0) //Seek to start of MFT on volume
	//log errors
	if errSeek != nil {
		log.Fatalf("Error occured when seeking MFT: %s\n", errSeek)
	}

	_, errRead := volC.Read(mft.selfRecord) //Store MFT
	//log errors
	if errRead != nil {
		log.Fatalf("Error occured when reading bytes in MFT self-record: %s\n", errRead)
	}

	volC.Close()

	//Create new Reader for MFT self record
	mft.reader = bytes.NewReader(mft.selfRecord)

	//Check that MFT Self-Record is valid (starts with FILE, not BAAD)
	startsigMFT := string(mft.readBytes(0, 4)) //starting signature in string
	if startsigMFT != "FILE" {
		log.Fatalf("Wrong starting signature for MFT Record: %s\n", startsigMFT)
	}

	//Obtain offset to first attribute
	offsetFirstAttr := int64(binary.LittleEndian.Uint16(mft.readBytes(20, 2)))

	//Jump to offset, check ID, determine size of attribute and next offset until reached data attribute
	var attrID byte
	var dataAttrID byte
	var offsetDataAttr int64

	attrIDSlice := make([]byte, 1)
	attrID = 0x00
	dataAttrID = 0x80
	offsetDataAttr = 0
	sizeAttr := offsetFirstAttr //to set up for loop and first offset

	for attrID != dataAttrID {
		offsetDataAttr += sizeAttr
		attrIDSlice = mft.readBytes(offsetDataAttr, 1)
		attrID = attrIDSlice[0]
		sizeAttr = int64(binary.LittleEndian.Uint32(mft.readBytes(offsetDataAttr+4, 4)))
	}

	//Parse Data Attribute of MFT Self-Record to obtain size and data runs
	offsetRuns := int64(binary.LittleEndian.Uint16(mft.readBytes(offsetDataAttr+32, 2))) //Offset to Data Runs in int64
	mft.sizeLogical = binary.LittleEndian.Uint64(mft.readBytes(offsetDataAttr+48, 8))    //Logical size of MFT

	//Extract Data Runs
	dataRunsRaw := mft.readBytes(offsetRuns+offsetDataAttr, int(sizeAttr-offsetRuns)) //read from start of data runs to end of data attribute

	fmt.Printf("Data Runs: %x\n", dataRunsRaw)

	//Set up slice of DataRun structs to parse data into
	mft.dataRunsAll = make([]DataRun, 0)

	//Create reader for data runs
	dataRunReader := bytes.NewReader(dataRunsRaw)

	//Until the end of the data runs (0), read control byte to read data runs and store in structs in mft.dataRunsAll
	datarun := DataRun{} //initialize single data run to add to mft.dataRunsAll
	ctrlByte, _ := dataRunReader.ReadByte()

	for ctrlByte != 0x00 {

		//Low nibble determines number of next bytes in data run that indicate number of contiguous clusters.
		//High nibble determines number of following bytes in data run that indicate cluster offset to data clusters.
		lenNumClusters := int(ctrlByte & 0x0F)
		lenClusterOffset := int(ctrlByte & 0xF0 >> 4)

		//Obtain number of clusters, reset  and pad with zeroes for uint64 conversion
		numClusters := make([]byte, lenNumClusters)
		dataRunReader.Read(numClusters)

		for len(numClusters) < 8 {
			numClusters = append(numClusters, 0x00)
		}

		//Convert number of contiguous bytes of data run and add to datarun struct
		datarun.numBytes = uint64(mft.bytesPerCluster) * binary.LittleEndian.Uint64(numClusters)

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

}

//MFT Method readBytes takes offset and number of bytes to read and returns slice of bytes
func (mft *MFT) readBytes(offset int64, bytes int) []byte {
	MFTreader := mft.reader //convert to variable to look pretty

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
		log.Fatalf("Error occured when reading MFT hexbytes: %s\n", errHex)
	}

	return hexbytes
}

//VBR Method parseVBR extracts key information about MFT from VBR on volume C:/ of native disk
func (vbr *VBR) parseVBR() {

	//Get handle on disk volume C
	pathToC := `\\.\C:`
	volC, errOpen := os.Open(pathToC)
	if errOpen != nil {
		log.Fatalf("Error while opening volume: %s\n", errOpen)
	}

	//Read VBR and store in struct, close handle on volume
	data := make([]byte, 512)
	_, errRead := volC.Read(data)
	if errRead != nil {
		log.Fatalf("Error occured when reading bytes in file: %s\n", errRead)
	}
	volC.Close()

	//Create new Reader for VBR data
	vbr.reader = bytes.NewReader(data)

	//Extract key data (listed in VBR struct) to determine key information about MFT

	bytesPerSectorHex := vbr.readBytes(11, 2)                               //hex representation
	vbr.BytesPerSector = int(binary.LittleEndian.Uint16(bytesPerSectorHex)) //convert to int

	sectorsPerClusterHex := vbr.readBytes(13, 2)                                  //hex representation
	vbr.SectorsPerCluster = int(binary.LittleEndian.Uint16(sectorsPerClusterHex)) //convert to int

	mftLogClusNumHex := vbr.readBytes(48, 8)                               //hex representation
	vbr.MFTLogClustNum = int(binary.LittleEndian.Uint64(mftLogClusNumHex)) //convert to int

	//if greater than 127, 2^(absolute value of negative representation)
	mftRecordSizeHex := vbr.readBytes(64, 1)
	if mftRecordSizeHex[0] < 0x80 {
		vbr.MFTRecordSize = int(mftRecordSizeHex[0])
	} else {
		vbr.MFTRecordSize = int(math.Exp2(math.Abs(float64(^mftRecordSizeHex[0] + 1))))
	}

}

//VBR Method readBytes takes offset and number of bytes to read and returns slice of bytes
func (vbr *VBR) readBytes(offset int64, bytes int) []byte {

	VBRreader := vbr.reader //convert to variable to look pretty

	//Seek to offset from beginning of data
	_, errSeek := VBRreader.Seek(offset, 0)
	//log errors
	if errSeek != nil {
		log.Fatalf("Error occured when seeking to offset of VBR field: %s\n", errSeek)
	}

	//Extract bytes of interest into slice hexbytes
	hexbytes := make([]byte, bytes)

	_, errHex := VBRreader.Read(hexbytes)
	//log error
	if errHex != nil {
		log.Fatalf("Error occured when reading hexbytes: %s\n", errHex)
	}

	return hexbytes
}
