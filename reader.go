package mobi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
)

type MobiReader struct {
	Mobi
}

func NewReader(filename string) (out *MobiReader, err error) {
	out = &MobiReader{}
	out.file, err = os.Open(filename)
	if err != nil {
		return nil, err
	}

	out.fileStat, err = out.file.Stat()
	if err != nil {
		return nil, err
	}

	return out, out.Parse()
}

func (r *MobiReader) Parse() (err error) {
	if err = r.parsePdf(); err != nil {
		return
	}

	if err = r.parsePdh(); err != nil {
		return
	}

	// Check if INDX offset is set + attempt to parse INDX
	if r.Header.IndxRecodOffset > 0 {
		err = r.parseIndexRecord(r.Header.IndxRecodOffset)
		if err != nil {
			return
		}
	}

	return
}

// parseHeader reads Palm Database Format header, and record offsets
func (r *MobiReader) parsePdf() error {
	//First we read PDF Header, this will help us parse subsequential data
	//binary.Read will take struct and fill it with data from mobi File
	err := binary.Read(r.file, binary.BigEndian, &r.Pdf)
	if err != nil {
		return err
	}

	if r.Pdf.RecordsNum < 1 {
		return errors.New("Number of records in this file is less than 1.")
	}

	r.Offsets = make([]mobiRecordOffset, r.Pdf.RecordsNum)
	err = binary.Read(r.file, binary.BigEndian, &r.Offsets)
	if err != nil {
		return err
	}

	//After the records offsets there's a 2 byte padding
	r.file.Seek(2, 1)

	return nil
}

// parsePdh processes record 0 that contains PalmDoc Header, Mobi Header and Exth meta data
func (r *MobiReader) parsePdh() error {
	// Palm Doc Header
	// Now we go onto reading record 0 that contains Palm Doc Header, Mobi Header, Exth Header...
	binary.Read(r.file, binary.BigEndian, &r.Pdh)

	// Check and see if there's a record encryption
	if r.Pdh.Encryption != 0 {
		return errors.New("Records are encrypted.")
	}

	// Mobi Header
	// Now it's time to read Mobi Header
	if r.MatchMagic(magicMobi) {
		binary.Read(r.file, binary.BigEndian, &r.Header)
	} else {
		return errors.New("Can not find MOBI header. File might be corrupt.")
	}

	// Current header struct only reads 232 bytes. So if actual header lenght is greater, then we need to skip to Exth.
	Skip := int64(r.Header.HeaderLength) - int64(reflect.TypeOf(r.Header).Size())
	r.file.Seek(Skip, 1)

	// Exth Record
	// To check whenever there's EXTH record or not, we need to check and see if 6th bit of r.Header.ExthFlags is set.
	if hasBit(int(r.Header.ExthFlags), 6) {
		err := r.ExthParse()

		if err != nil {
			return errors.New("Can not read EXTH record")
		}
	}

	return nil
}

func (r *MobiReader) parseIndexRecord(n uint32) error {
	_, err := r.OffsetToRecord(n)
	if err != nil {
		return err
	}

	RecPos, _ := r.file.Seek(0, 1)

	if !r.MatchMagic(magicIndx) {
		return errors.New("Index record not found at specified at given offset")
	}
	//fmt.Printf("Index %s %v\n", r.Peek(4), RecLen)

	//if len(r.Indx) == 0 {
	r.Indx = append(r.Indx, mobiIndx{})
	//}

	idx := &r.Indx[len(r.Indx)-1]

	err = binary.Read(r.file, binary.BigEndian, idx)
	if err != nil {
		return err
	}

	/* Tagx Record Parsing + Last CNCX */
	if idx.Tagx_Offset != 0 {
		_, err = r.file.Seek(RecPos+int64(idx.Tagx_Offset), 0)
		if err != nil {
			return err
		}

		err = r.parseTagx()
		if err != nil {
			return err
		}

		// Last CNCX record follows TAGX
		if idx.Cncx_Records_Count > 0 {
			r.Cncx = mobiCncx{}
			binary.Read(r.file, binary.BigEndian, &r.Cncx.Len)

			r.Cncx.Id = make([]uint8, r.Cncx.Len)
			binary.Read(r.file, binary.LittleEndian, &r.Cncx.Id)
			r.file.Seek(1, 1) //Skip 0x0 termination

			binary.Read(r.file, binary.BigEndian, &r.Cncx.NCX_Count)

			// PrintStruct(r.Cncx)
		}
	}

	/* Ordt Record Parsing */
	if idx.Idxt_Encoding == MOBI_ENC_UTF16 || idx.Ordt_Entries_Count > 0 {
		return errors.New("ORDT parser not implemented")
	}

	/* Ligt Record Parsing */
	if idx.Ligt_Entries_Count > 0 {
		return errors.New("LIGT parser not implemented")
	}

	/* Idxt Record Parsing */
	if idx.Idxt_Count > 0 {
		_, err = r.file.Seek(RecPos+int64(idx.Idxt_Offset), 0)
		if err != nil {
			return err
		}

		err = r.parseIdxt(idx.Idxt_Count)
		if err != nil {
			return err
		}
	}

	//CNCX Data?
	var Count = 0
	if idx.Indx_Type == INDX_TYPE_NORMAL {
		//r.file.Seek(RecPos+int64(idx.HeaderLen), 0)

		var PTagxLen = []uint8{0}
		for i, offset := range r.Idxt.Offset {
			r.file.Seek(RecPos+int64(offset), 0)

			// Read Byte containing the lenght of a label
			r.file.Read(PTagxLen)

			// Read label
			PTagxLabel := make([]uint8, PTagxLen[0])
			r.file.Read(PTagxLabel)

			PTagxLen1 := uint16(idx.Idxt_Offset) - r.Idxt.Offset[i]
			if i+1 < len(r.Idxt.Offset) {
				PTagxLen1 = r.Idxt.Offset[i+1] - r.Idxt.Offset[i]
			}

			PTagxData := make([]uint8, PTagxLen1)
			r.file.Read(PTagxData)
			fmt.Printf("\n------ %v --------\n", i)
			r.parsePtagx(PTagxData)
			Count++
			//fmt.Printf("Len: %v | Label: %s | %v\n", PTagxLen, PTagxLabel, Count)
		}
	}

	// Check next record
	//r.OffsetToRecord(n + 1)

	//
	// Process remaining INDX records
	if idx.Indx_Type == INDX_TYPE_INFLECTION {
		r.parseIndexRecord(n + 1)
	}
	//fmt.Printf("%s", )
	// Read Tagx
	//		if idx.Tagx_Offset > 0 {
	//			err := r.parseTagx()
	//			if err != nil {
	//				return err
	//			}
	//		}

	return nil
}

// MatchMagic matches next N bytes (based on lenght of magic word)
func (r *MobiReader) MatchMagic(magic mobiMagicType) bool {
	if r.Peek(len(magic)).Magic() == magic {
		return true
	}
	return false
}

// Peek returns next N bytes without advancing the reader.
func (r *MobiReader) Peek(n int) Peeker {
	buf := make([]uint8, n)
	r.file.Read(buf)
	r.file.Seek(int64(n)*-1, 1)
	return buf
}

// Parse reads/parses Exth meta data records from file
func (r *MobiReader) ExthParse() error {
	// If next 4 bytes are not EXTH then we have a problem
	if !r.MatchMagic(magicExth) {
		return errors.New("Currect reading position does not contain EXTH record")
	}

	binary.Read(r.file, binary.BigEndian, &r.Exth.Identifier)
	binary.Read(r.file, binary.BigEndian, &r.Exth.HeaderLenght)
	binary.Read(r.file, binary.BigEndian, &r.Exth.RecordCount)

	r.Exth.Records = make([]mobiExthRecord, r.Exth.RecordCount)
	for i, _ := range r.Exth.Records {
		binary.Read(r.file, binary.BigEndian, &r.Exth.Records[i].RecordType)
		binary.Read(r.file, binary.BigEndian, &r.Exth.Records[i].RecordLength)

		r.Exth.Records[i].Value = make([]uint8, r.Exth.Records[i].RecordLength-8)

		Tag := getExthMetaByTag(r.Exth.Records[i].RecordType)
		switch Tag.Type {
		case EXTH_TYPE_BINARY:
			binary.Read(r.file, binary.BigEndian, &r.Exth.Records[i].Value)
			//			fmt.Printf("%v: %v\n", Tag.Name, r.Exth.Records[i].Value)
		case EXTH_TYPE_STRING:
			binary.Read(r.file, binary.LittleEndian, &r.Exth.Records[i].Value)
			//			fmt.Printf("%v: %s\n", Tag.Name, r.Exth.Records[i].Value)
		case EXTH_TYPE_NUMERIC:
			binary.Read(r.file, binary.BigEndian, &r.Exth.Records[i].Value)
			//			fmt.Printf("%v: %d\n", Tag.Name, binary.BigEndian.Uint32(r.Exth.Records[i].Value))
		}
	}

	return nil
}

// OffsetToRecord sets reading position to record N, returns total record lenght
func (r *MobiReader) OffsetToRecord(nu uint32) (uint32, error) {
	n := int(nu)
	if n > int(r.Pdf.RecordsNum)-1 {
		return 0, errors.New("Record ID requested is greater than total amount of records")
	}

	RecLen := uint32(0)
	if n+1 < int(r.Pdf.RecordsNum) {
		RecLen = r.Offsets[n+1].Offset
	} else {
		RecLen = uint32(r.fileStat.Size())
	}

	_, err := r.file.Seek(int64(r.Offsets[n].Offset), 0)

	return RecLen - r.Offsets[n].Offset, err
}

func (r *MobiReader) parseTagx() error {
	if !r.MatchMagic(magicTagx) {
		return errors.New("TAGX record not found at given offset.")
	}

	r.Tagx = mobiTagx{}

	binary.Read(r.file, binary.BigEndian, &r.Tagx.Identifier)
	binary.Read(r.file, binary.BigEndian, &r.Tagx.HeaderLenght)
	if r.Tagx.HeaderLenght < 12 {
		return errors.New("TAGX record too short")
	}
	binary.Read(r.file, binary.BigEndian, &r.Tagx.ControlByteCount)

	TagCount := (r.Tagx.HeaderLenght - 12) / 4
	r.Tagx.Tags = make([]mobiTagxTags, TagCount)

	for i := 0; i < int(TagCount); i++ {
		err := binary.Read(r.file, binary.BigEndian, &r.Tagx.Tags[i])
		if err != nil {
			return err
		}
	}

	fmt.Println("TagX called")
	// PrintStruct(r.Tagx)

	return nil
}

func (r *MobiReader) parseIdxt(IdxtCount uint32) error {
	fmt.Println("parseIdxt called")
	if !r.MatchMagic(magicIdxt) {
		return errors.New("IDXT record not found at given offset.")
	}

	binary.Read(r.file, binary.BigEndian, &r.Idxt.Identifier)

	r.Idxt.Offset = make([]uint16, IdxtCount)

	binary.Read(r.file, binary.BigEndian, &r.Idxt.Offset)
	//for id, _ := range r.Idxt.Offset {
	//	binary.Read(r.Buffer, binary.BigEndian, &r.Idxt.Offset[id])
	//}

	//Skip two bytes? Or skip necessary amount to reach total lenght in multiples of 4?
	r.file.Seek(2, 1)

	// PrintStruct(r.Idxt)
	return nil
}

func (r *MobiReader) parsePtagx(data []byte) {
	//control_byte_count
	//tagx
	control_bytes := data[:r.Tagx.ControlByteCount]
	data = data[r.Tagx.ControlByteCount:]

	var Ptagx []mobiPTagx //= make([]mobiPTagx, r.Tagx.TagCount())

	for _, x := range r.Tagx.Tags {
		if x.Control_Byte == 0x01 {
			control_bytes = control_bytes[1:]
			continue
		}

		value := control_bytes[0] & x.Bitmask
		if value != 0 {
			var value_count uint32
			var value_bytes uint32

			if value == x.Bitmask {
				if setBits[x.Bitmask] > 1 {
					// If all bits of masked value are set and the mask has more
					// than one bit, a variable width value will follow after
					// the control bytes which defines the length of bytes (NOT
					// the value count!) which will contain the corresponding
					// variable width values.
					var consumed uint32
					value_bytes, consumed = vwiDec(data, true)
					//fmt.Printf("\nConsumed %v", consumed)
					data = data[consumed:]
				} else {
					value_count = 1
				}
			} else {
				mask := x.Bitmask
				for {
					if mask&1 != 0 {
						//fmt.Printf("Break")
						break
					}
					mask >>= 1
					value >>= 1
				}
				value_count = uint32(value)
			}

			Ptagx = append(Ptagx, mobiPTagx{x.Tag, x.TagNum, value_count, value_bytes})
			//						ptagx[ptagx_count].tag = tagx->tags[i].tag;
			//       ptagx[ptagx_count].tag_value_count = tagx->tags[i].values_count;
			//       ptagx[ptagx_count].value_count = value_count;
			//       ptagx[ptagx_count].value_bytes = value_bytes;

			//fmt.Printf("TAGX %v %v VC:%v VB:%v\n", x.Tag, x.TagNum, value_count, value_bytes)
		}
	}
	fmt.Printf("%+v", Ptagx)
	var IndxEntry []mobiIndxEntry
	for iz, x := range Ptagx {
		var values []uint32

		if x.Value_Count != 0 {
			// Read value_count * values_per_entry variable width values.
			fmt.Printf("\nDec: ")
			for i := 0; i < int(x.Value_Count)*int(x.Tag_Value_Count); i++ {
				byts, consumed := vwiDec(data, true)
				data = data[consumed:]

				values = append(values, byts)
				IndxEntry = append(IndxEntry, mobiIndxEntry{x.Tag, byts})
				fmt.Printf("%v %s: %v ", iz, tagEntryMap[x.Tag], byts)
			}
		} else {
			// Convert value_bytes to variable width values.
			total_consumed := 0
			for {
				if total_consumed < int(x.Value_Bytes) {
					byts, consumed := vwiDec(data, true)
					data = data[consumed:]

					total_consumed += int(consumed)

					values = append(values, byts)
					IndxEntry = append(IndxEntry, mobiIndxEntry{x.Tag, byts})
				} else {
					break
				}
			}
			if total_consumed != int(x.Value_Bytes) {
				panic("Error not enough bytes are consumed. Consumed " + strconv.Itoa(total_consumed) + " out of " + strconv.Itoa(int(x.Value_Bytes)))
			}
		}
	}
	fmt.Println("---------------------------")
}
