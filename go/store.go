package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	_ "errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	FormatHtml     = 0
	FormatTextile  = 1
	FormatMarkdown = 2
	FormatText     = 3

	FormatFirst = 0
	FormatLast  = 3
)

type Text struct {
	Id        int
	CreatedOn time.Time
	Format    int
	Sha1      [20]byte
}

type Article struct {
	Id         int
	Permalink1 string
	Permalink2 string
	IsPrivate  bool
	IsDeleted  bool
	Title      string
	Tags       []string
	Versions   []*Text
}

type Store struct {
	sync.Mutex
	dataDir string

	texts        []Text
	textIdToText map[int]*Text

	articles           []Article
	articleIdToArticle map[int]*Article
	dataFile           *os.File
}

func validFormat(format int) bool {
	return format >= FormatFirst && format <= FormatLast
}

// parse:
// T1|1234860514|0|OiKDjvc+iyv4UXxVxLO91ozXwaU
func (s *Store) parseText(line []byte) {
	parts := strings.Split(string(line[1:]), "|")
	if len(parts) != 4 {
		panic("len(parts) != 4")
	}
	idStr := parts[0]
	createdOnSecondsStr := parts[1]
	formatStr := parts[2]
	msgSha1b64 := parts[3] + "="

	id, err := strconv.Atoi(idStr)
	if err != nil {
		panic("idStr not a number")
	}
	if _, ok := s.textIdToText[id]; ok {
		panic("parseText(): duplicate Text id")
	}

	createdOnSeconds, err := strconv.Atoi(createdOnSecondsStr)
	if err != nil {
		panic("createdOnSeconds not a number")
	}
	createdOn := time.Unix(int64(createdOnSeconds), 0)

	format, err := strconv.Atoi(formatStr)

	if err != nil || !validFormat(format) {
		panic("format not a number or invalid")
	}

	msgSha1, err := base64.StdEncoding.DecodeString(msgSha1b64)
	if err != nil {
		panic("msgSha1b64 not valid base64")
	}
	if len(msgSha1) != 20 {
		panic("len(msgSha1) != 20")
	}

	t := Text{
		Id:        id,
		CreatedOn: createdOn,
		Format:    format,
	}
	copy(t.Sha1[:], msgSha1)
	if !s.MessageFileExists(t.Sha1) {
		panic("message file doesn't exist")
	}

	s.texts = append(s.texts, t)
	s.textIdToText[id] = &s.texts[len(s.texts)-1]
}

func strToBool(s string) bool {
	if s == "" {
		return false
	}
	if s == "1" {
		return true
	}
	panic("invalid bool string")
}

// parse:
// A582|$permalink1|$permalink2|$isPublic|$isDeleted|$title|$tags|$versions
func (s *Store) parseArticle(line []byte) {
	parts := strings.Split(string(line[1:]), "|")
	if len(parts) != 8 {
		panic("len(parts) != 8")
	}
	idStr := parts[0]
	permalink1 := parts[1]
	permalink2 := parts[2]
	isPublicStr := parts[3]
	isDeletedStr := parts[4]
	title := parts[5]
	tagsStr := parts[6]
	versionIdsStr := parts[7]

	id, err := strconv.Atoi(idStr)
	if err != nil {
		panic("idStr not a number")
	}
	if _, ok := s.articleIdToArticle[id]; ok {
		panic("duplicate Article id")
	}
	isPublic := strToBool(isPublicStr)
	isDeleted := strToBool(isDeletedStr)
	tags := strings.Split(tagsStr, ",")

	versionsStr := strings.Split(versionIdsStr, ",")
	nVersions := len(versionsStr)
	if nVersions == 0 {
		panic("We need some versions")
	}

	a := Article{
		Id:         id,
		Permalink1: permalink1,
		Permalink2: permalink2,
		IsPrivate:  !isPublic,
		IsDeleted:  isDeleted,
		Title:      title,
		Tags:       tags,
		Versions:   make([]*Text, nVersions, nVersions),
	}

	for i, verStr := range versionsStr {
		id, err = strconv.Atoi(verStr)
		if err != nil {
			panic("verStr not a number")
		}
		if txt, ok := s.textIdToText[id]; !ok {
			panic("non-existent verStr")
		} else {
			a.Versions[i] = txt
		}
	}

	s.articles = append(s.articles, a)
	s.articleIdToArticle[id] = &s.articles[len(s.articles)-1]
}

func (s *Store) readExistingBlogData(fileDataPath string) error {
	d, err := ReadFileAll(fileDataPath)
	if err != nil {
		return err
	}

	for len(d) > 0 {
		idx := bytes.IndexByte(d, '\n')
		if -1 == idx {
			// TODO: this could happen if the last record was only
			// partially written. Should I just ignore it?
			panic("idx shouldn't be -1")
		}
		line := d[:idx]
		d = d[idx+1:]
		c := line[0]
		if c == 'T' {
			s.parseText(line)
		} else if c == 'A' {
			s.parseArticle(line)
		} else {
			panic("Unexpected line type")
		}
	}
	return nil
}

func NewStore(dataDir string) (*Store, error) {
	dataFilePath := filepath.Join(dataDir, "blogdata.txt")
	store := &Store{
		dataDir:            dataDir,
		texts:              make([]Text, 0),
		articles:           make([]Article, 0),
		articleIdToArticle: make(map[int]*Article),
		textIdToText:       make(map[int]*Text),
	}
	var err error
	if PathExists(dataFilePath) {
		err = store.readExistingBlogData(dataFilePath)
		if err != nil {
			fmt.Printf("NewStore(): readExistingBlogData() failed with %s\n", err.Error())
			return nil, err
		}
	} else {
		f, err := os.Create(dataFilePath)
		if err != nil {
			fmt.Printf("NewStore(): os.Create(%s) failed with %s", dataFilePath, err.Error())
			return nil, err
		}
		f.Close()
	}
	store.dataFile, err = os.OpenFile(dataFilePath, os.O_APPEND|os.O_RDWR, 0666)
	if err != nil {
		fmt.Printf("NewStore(): os.OpenFile(%s) failed with %s", dataFilePath, err.Error())
		return nil, err
	}
	logger.Noticef("texts: %d, articles: %d", len(store.texts), len(store.articles))
	return store, nil
}

func (s *Store) ArticlesCount() int {
	s.Lock()
	defer s.Unlock()
	return len(s.articles)
}

func blobPath(dir, sha1 string) string {
	d1 := sha1[:2]
	d2 := sha1[2:4]
	return filepath.Join(dir, "blobs", d1, d2, sha1)
}

func (s *Store) MessageFilePath(sha1 [20]byte) string {
	sha1Str := hex.EncodeToString(sha1[:])
	return blobPath(s.dataDir, sha1Str)
}

func (s *Store) MessageFileExists(sha1 [20]byte) bool {
	p := s.MessageFilePath(sha1)
	return PathExists(p)
}

func (s *Store) appendString(str string) error {
	_, err := s.dataFile.WriteString(str)
	if err != nil {
		fmt.Printf("appendString() error: %s\n", err.Error())
	}
	return err
}

func remSep(s string) string {
	return strings.Replace(s, "|", "", -1)
}

func (s *Store) writeMessageAsSha1(msg []byte, sha1 [20]byte) error {
	path := s.MessageFilePath(sha1)
	err := WriteBytesToFile(msg, path)
	if err != nil {
		logger.Errorf("Store.writeMessageAsSha1(): failed to write %s with error %s", path, err.Error())
	}
	return err
}
