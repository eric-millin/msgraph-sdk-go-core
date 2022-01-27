package msgraphgocore

import (
	"errors"
	"net/url"
	"reflect"
	"unsafe"

	abstractions "github.com/microsoft/kiota/abstractions/go"
	"github.com/microsoft/kiota/abstractions/go/serialization"
)

type Page interface {
	getValue() []interface{}
	getNextLink() *string
}

// PageIterator represents an iterator object that can be used to get subsequent pages of a collection.
type PageIterator struct {
	currentPage     Page
	reqAdapter      GraphRequestAdapterBase
	pauseIndex      int
	constructorFunc ParsableConstructor
	headers         map[string]string
	reqOptions      []abstractions.RequestOption
}

type ParsableConstructor func() serialization.Parsable

type PageResult struct {
	nextLink *string
	value    []interface{}
}

func (p *PageResult) getValue() []interface{} {
	if p == nil {
		return nil
	}

	return p.value
}

func (p *PageResult) getNextLink() *string {
	if p == nil {
		return nil
	}

	return p.nextLink
}

// NewpageIterator creates an iterator instance
//
// It has three parameters. res is the graph response from the initial request and represents the first page.
// reqAdapter is used for getting the next page and constructorFunc is used for serializing next page's response to the specified type.
func NewPageIterator(res interface{}, reqAdapter GraphRequestAdapterBase, constructorFunc ParsableConstructor) (*PageIterator, error) {
	page, err := convertToPage(res)
	if err != nil {
		return nil, err
	}

	return &PageIterator{
		currentPage:     page,
		reqAdapter:      reqAdapter,
		pauseIndex:      0,
		constructorFunc: constructorFunc,
		headers:         map[string]string{},
	}, nil
}

// Iterate traverses all pages and enumerates all items in the current page and returns an error if something goes wrong.
//
// Iterate receives a callback function which is called with each item in the current page as an argument. The callback function
// returns a boolean. To traverse and enumerate all pages always return true and to pause traversal and enumeration
// return false from the callback.
//
// Example
//      pageIterator, err := NewPageIterator(resp, reqAdapter, parsableCons)
//      callbackFunc := func (pageItem interface{}) bool {
//          fmt.Println(pageitem.GetDisplayName())
//          return true
//      }
//      err := pageIterator.Iterate(callbackFunc)
func (pI *PageIterator) Iterate(callback func(pageItem interface{}) bool) error {
	for pI.currentPage != nil {
		keepIterating := pI.enumerate(callback)

		if !keepIterating {
			// Callback returned false, stop iterating through pages.
			return nil
		}

		err := pI.next()
		if err != nil {
			return err
		}
		pI.pauseIndex = 0 // when moving to the next page reset pauseIndex
	}

	return nil
}

// SetHeaders provides headers for requests made to get subsequent pages
//
// Headers in the initial request -- request to get the first page -- are not included in subsequent page requests.
func (pI *PageIterator) SetHeaders(headers map[string]string) {
	pI.headers = headers
}

// SetReqOptions provides configuration for handlers during requests for subsequent pages
func (pI *PageIterator) SetReqOptions(reqOptions []abstractions.RequestOption) {
	pI.reqOptions = reqOptions
}

func (pI *PageIterator) hasNext() bool {
	if pI.currentPage == nil || pI.currentPage.getNextLink() == nil {
		return false
	}
	return true
}

func (pI *PageIterator) next() error {
	nextPage, err := pI.getNextPage()
	if err != nil {
		return err
	}

	pI.currentPage = nextPage
	return nil
}

func (pI *PageIterator) getNextPage() (*PageResult, error) {
	if pI.currentPage.getNextLink() == nil {
		return nil, nil
	}

	nextLink, err := url.Parse(*pI.currentPage.getNextLink())
	if err != nil {
		return nil, errors.New("Parsing nextLink url failed")
	}

	requestInfo := abstractions.NewRequestInformation()
	requestInfo.Method = abstractions.GET
	requestInfo.SetUri(*nextLink)
	requestInfo.Headers = pI.headers
	requestInfo.AddRequestOptions(pI.reqOptions...)

	res, err := pI.reqAdapter.SendAsync(*requestInfo, pI.constructorFunc, nil)
	if err != nil {
		return nil, errors.New("Fetching next page failed")
	}

	return convertToPage(res)
}

func (pI *PageIterator) enumerate(callback func(item interface{}) bool) bool {
	keepIterating := true

	if pI.currentPage == nil {
		return false
	}

	pageItems := pI.currentPage.getValue()
	if pageItems == nil {
		return false
	}

	if pI.pauseIndex >= len(pageItems) {
		return false
	}

	// start/continue enumerating page items from  pauseIndex.
	// this makes it possible to resume iteration from where we paused iteration.
	for i := pI.pauseIndex; i < len(pageItems); i++ {
		keepIterating = callback(pageItems[i])

		if !keepIterating {
			// Callback returned false, pause! stop enumerating page items. Set pauseIndex so that we know
			// where to resume from.
			// Resumes from the next item
			pI.pauseIndex = i + 1
			break
		}
	}

	return keepIterating
}

func convertToPage(response interface{}) (*PageResult, error) {
	if response == nil {
		return nil, errors.New("response cannot be nil")
	}
	ref := reflect.ValueOf(response).Elem()

	value := ref.FieldByName("value")
	if value.IsNil() {
		return nil, errors.New("value property missing in response object")
	}
	value = reflect.NewAt(value.Type(), unsafe.Pointer(value.UnsafeAddr())).Elem()

	nextLink := ref.FieldByName("nextLink")
	var link *string
	if !nextLink.IsNil() {
		nextLink = reflect.NewAt(nextLink.Type(), unsafe.Pointer(nextLink.UnsafeAddr())).Elem()
		link = nextLink.Interface().(*string)
	}

	// Collect all entities in the value slice.
	// This converts a graph slice ie []graph.User to a dynamic slice []interface{}
	collected := make([]interface{}, 0)
	for i := 0; i < value.Len(); i++ {
		collected = append(collected, value.Index(i).Interface())
	}

	return &PageResult{
		nextLink: link,
		value:    collected,
	}, nil
}
