/*
Copyright (c) 2016 IBM Corporation and other Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and limitations under the License.

Contributors:
Kim Letkeman - Initial Contribution
*/

// v1 KL 09 Aug 2016 Creation of assetUtils as boilerplate for any asset to call for standard
//                   crud like behaviors. Make extensive use of crudUtils.
// v2 KL 02 Nov 2016 new package ctasset

package ctasset

import (
	"encoding/json"
	// "errors"
	"fmt"
	"time"

	"github.com/hyperledger/fabric/core/chaincode/shim"
	// cf "github.com/ibm-watson-iot/blockchain-samples/iotbase/ctconfig"
	// h "github.com/ibm-watson-iot/blockchain-samples/iotbase/cthistory"
	"sort"

	st "github.com/ibm-watson-iot/blockchain-samples/iotbase/ctstate"
)

// InvokeEvent carries the event that is to be set upon exit from the chaincode
type InvokeEvent struct {
	Name    string                 `json:"name"`
	Payload map[string]interface{} `json:"payload"`
}

// AssetClass defines a receiver for rules and other class-specific execution
type AssetClass struct {
	Name        string `json:"name"`        // asset class name
	Prefix      string `json:"prefix"`      // asset class prefix for key in world state
	AssetIDPath string `json:"assetIDpath"` // property that is unique key for this class
}

// NewAsset create an instance of an asset class
func (c AssetClass) NewAsset() Asset {
	var a = Asset{
		c, "", nil, nil, "", "", nil, nil, nil, true,
	}
	return a
}

// AllAssetClass is the class of all assets
var AllAssetClass = AssetClass{"All", "", ""}

func (c AssetClass) String() string {
	return fmt.Sprintf("CLS=%s | PRF=%s | ID=%s", c.Name, c.Prefix, c.AssetIDPath)
}

// Asset is a type that holds all information about an asset, including its name,
// its world state prefix, and the qualified property name that is its assetID
type Asset struct {
	Class      AssetClass              `json:"assetclass"` // this asset's class
	AssetKey   string                  `json:"assetkey"`   // world state key prefixed
	State      *map[string]interface{} `json:"state"`      // current state
	EventIn    *map[string]interface{} `json:"eventin"`    // most recent event
	FunctionIn string                  `json:"functionin"` // function that created this state
	TXNID      string                  `json:"txnid"`      // transaction UUID linking back to blockchain
	TXNTS      *time.Time              `json:"txnts"`      // transaction timestamp matching blockchain
	EventOut   *InvokeEvent            `json:"eventout"`   // event (if any) emitted upon exit from the invoke
	Alerts     *[]string               `json:"alerts"`     // array of active alerts
	Compliant  bool                    `json:"compliant"`  // true if the asset complies with the contract terms
}

// AssetArray is an array of assets, used by read all, recent states, history, etc.
type AssetArray []Asset

func (a Asset) String() string {
	return st.PrettyPrint(a)
}

func (aa AssetArray) String() string {
	return st.PrettyPrint(aa)
}

// Logger for the ctstate package
var log = shim.NewLogger("asst")

// CreateAsset inializes a new asset and stores it in world state
func (c *AssetClass) CreateAsset(stub shim.ChaincodeStubInterface, args []string, caller string, inject []QPropNV) ([]byte, error) {

	var a = c.NewAsset()

	if err := a.unmarshallEventIn(stub, args); err != nil {
		err = fmt.Errorf("CreateAsset for class %s could not unmarshall, err is %s", c.Name, err)
		log.Errorf(err.Error())
		return nil, err
	}
	assetKey, err := a.getAssetKey()
	if err != nil {
		err = fmt.Errorf("CreateAsset for class %s could not find id at %s, err is %s", c.Name, c.AssetIDPath, err)
		log.Errorf(err.Error())
		return nil, err
	}
	_, exists, err := c.getAssetFromWorldState(stub, assetKey)
	if err != nil {
		err := fmt.Errorf("CreateAsset for class %s asset %s read from world state returned error %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	if exists {
		err := fmt.Errorf("CreateAsset for class %s asset %s asset already exists", c.Name, a.AssetKey)
		log.Errorf(err.Error())
		return nil, err
	}

	// copy the event into a new state
	astate := st.DeepMergeMap(*a.EventIn, make(map[string]interface{}))
	a.State = &astate
	if err := a.addTXNTimestampToState(stub); err != nil {
		err = fmt.Errorf("CreateAsset for class %s failed to add txn timestamp for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}

	// save original asset function
	a.FunctionIn = caller

	if len(inject) > 0 {
		err := a.injectProps(inject)
		if err != nil {
			err = fmt.Errorf("CreateAsset for class %s failed to inject properties %+v for %s, err is %s", c.Name, inject, a.AssetKey, err)
			log.Errorf(err.Error())
			return nil, err
		}
	}
	if err := a.handleAlertsAndRules(stub); err != nil {
		err = fmt.Errorf("CreateAsset for class %s failed in rules engine for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	if err := a.putMarshalledState(stub); err != nil {
		err = fmt.Errorf("CreateAsset for class %s failed to to marshall for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	return nil, nil
}

// UpdateAsset updates an asset and stores it in world state
func (c *AssetClass) UpdateAsset(stub shim.ChaincodeStubInterface, args []string, caller string, inject []QPropNV) ([]byte, error) {

	var arg = c.NewAsset()
	var a = c.NewAsset()

	if err := arg.unmarshallEventIn(stub, args); err != nil {
		err = fmt.Errorf("UpdateAsset for class %s could not unmarshall, err is %s", c.Name, err)
		log.Errorf(err.Error())
		return nil, err
	}
	assetKey, err := arg.getAssetKey()
	if err != nil {
		err = fmt.Errorf("UpdateAsset for class %s could not find id at %s, err is %s", c.Name, c.AssetIDPath, err)
		log.Errorf(err.Error())
		return nil, err
	}
	log.Debugf("UpdateAsset arg struct= %=v", arg)
	assetBytes, exists, err := c.getAssetFromWorldState(stub, assetKey)
	if err != nil {
		err := fmt.Errorf("UpdateAsset for class %s asset %s read from world state returned error %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	if !exists {
		err := fmt.Errorf("UpdateAsset for class %s asset %s asset does not exist", c.Name, a.AssetKey)
		log.Errorf(err.Error())
		return nil, err
	}
	err = json.Unmarshal(assetBytes, &a)
	if err != nil {
		err := fmt.Errorf("UpdateAsset for class %s asset %s Unmarshal failed with err %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	log.Debugf("UpdateAsset asset struct= %+v", a)
	// save the incoming EventIn
	a.EventIn = arg.EventIn
	a.FunctionIn = arg.FunctionIn
	log.Debugf("UpdateAsset asset struct after saving EventIn= %+v", a)

	// merge the event into the state
	astate := st.DeepMergeMap(*a.EventIn, *a.State)
	a.State = &astate
	log.Debugf("UpdateAsset asset struct after deepMerge= %+v", a)

	if err := a.addTXNTimestampToState(stub); err != nil {
		err = fmt.Errorf("UpdateAsset for class %s failed to add txn timestamp for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}

	// save original asset function
	a.FunctionIn = caller

	if len(inject) > 0 {
		err := a.injectProps(inject)
		if err != nil {
			err = fmt.Errorf("UpdateAsset for class %s failed to inject properties %+v for %s, err is %s", c.Name, inject, a.AssetKey, err)
			log.Errorf(err.Error())
			return nil, err
		}
	}
	if err := a.handleAlertsAndRules(stub); err != nil {
		err = fmt.Errorf("UpdateAsset for class %s failed in rules engine for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	if err := a.putMarshalledState(stub); err != nil {
		err = fmt.Errorf("CreateAsset for class %s failed to to marshall for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}

	log.Debugf("UpdateAsset asset struct final= %+v", a)
	return nil, nil
}

// // DeleteAsset deletes an asset from world state
// func DeleteAsset(stub shim.ChaincodeStubInterface, args []string, assetName string, caller string) ([]byte, error) {
//     argsMap, err := getUnmarshalledArgument(stub, caller, args)
//     if err != nil {
//         return nil, err
//     }
//     assetID, err := validateAssetID(caller, assetName, argsMap)
//     if err != nil {
//         return nil, err
//     }
//     err = removeOneAssetFromWorldState(stub, caller, assetName, assetID)
//     if err != nil {
//         return nil, err
//     }
//     return nil, nil
// }

// // DeleteAllAssets reletes all asstes of a specific asset class from world state
// func DeleteAllAssets(stub shim.ChaincodeStubInterface, args []string, assetName string, caller string) ([]byte, error) {
//     var err error
//     prefix, err := cf.EventNameToAssetPrefix(assetName)
//     if err != nil {
//         return nil, err
//     }
//     iter, err := stub.RangeQueryState(prefix, prefix+"}")
//     if err != nil {
//         err = fmt.Errorf("deleteAllAssets failed to get a range query iterator: %s", err)
//         log.Errorf(err.Error())
//         return nil, err
//     }
//     defer iter.Close()
//     for iter.HasNext() {
//         assetID, _, err := iter.Next()
//         if err != nil {
//             err = fmt.Errorf("deleteAllAssets iter.Next() failed: %s", err)
//             log.Errorf(err.Error())
//             return nil, err
//         }
//         err = removeOneAssetFromWorldState(stub, caller, assetName, assetID)
//         if err != nil {
//             err = fmt.Errorf("deleteAllAssets%s failed to remove an asset: %s", assetName, err)
//             log.Errorf(err.Error())
//             // continue best efforts?
//         }
//     }
//     return nil, nil
// }

// DeletePropertiesFromAsset removes specific properties from an asset in world state
func (c *AssetClass) DeletePropertiesFromAsset(stub shim.ChaincodeStubInterface, args []string, caller string, inject []QPropNV) ([]byte, error) {

	var arg = c.NewAsset()
	var a = c.NewAsset()

	if err := arg.unmarshallEventIn(stub, args); err != nil {
		err = fmt.Errorf("DeletePropertiesFromAsset for class %s could not unmarshall, err is %s", c.Name, err)
		log.Errorf(err.Error())
		return nil, err
	}
	assetKey, err := arg.getAssetKey()
	if err != nil {
		err = fmt.Errorf("DeletePropertiesFromAsset for class %s could not find id at %s, err is %s", c.Name, c.AssetIDPath, err)
		log.Errorf(err.Error())
		return nil, err
	}
	assetBytes, exists, err := c.getAssetFromWorldState(stub, assetKey)
	if err != nil {
		err := fmt.Errorf("DeletePropertiesFromAsset for class %s asset %s read from world state returned error %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	if !exists {
		err := fmt.Errorf("DeletePropertiesFromAsset for class %s asset %s asset does not exist", c.Name, a.AssetKey)
		log.Errorf(err.Error())
		return nil, err
	}
	err = json.Unmarshal(assetBytes, &a)
	if err != nil {
		err := fmt.Errorf("DeletePropertiesFromAsset for class %s asset %s Unmarshal failed with err %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	// save the incoming EventIn
	a.EventIn = arg.EventIn
	a.FunctionIn = arg.FunctionIn
	log.Debugf("DeletePropertiesFromAsset asset struct after saving EventIn= %+v", a)

	var qprops []string
	qprops, found := st.GetObjectAsStringArray(arg.EventIn, "qprops")
	if !found {
		err = fmt.Errorf("deletePropertiesFromAsset asset %s has no qprops argument or qprops not a string array", assetKey)
		log.Errorf(err.Error())
		return nil, err
	}

	// remove qualified properties from state
	for _, p := range qprops {
		_ = st.RemoveObject(a.State, p)
	}

	if err := a.addTXNTimestampToState(stub); err != nil {
		err = fmt.Errorf("deletePropertiesFromAsset for class %s failed to add txn timestamp for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}

	// save original asset function
	a.FunctionIn = caller

	if len(inject) > 0 {
		err := a.injectProps(inject)
		if err != nil {
			err = fmt.Errorf("deletePropertiesFromAsset for class %s failed to inject properties %+v for %s, err is %s", c.Name, inject, a.AssetKey, err)
			log.Errorf(err.Error())
			return nil, err
		}
	}
	if err := a.handleAlertsAndRules(stub); err != nil {
		err = fmt.Errorf("CreateAsset for class %s failed in rules engine for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}
	if err := a.putMarshalledState(stub); err != nil {
		err = fmt.Errorf("CreateAsset for class %s failed to to marshall for %s, err is %s", c.Name, a.AssetKey, err)
		log.Errorf(err.Error())
		return nil, err
	}

	// log.Debugf("UpdateAsset asset struct final= %+v", a)
	return nil, nil
}

// ReadAsset returns an asset from world state, intended to be returned directly to a client
func (c *AssetClass) ReadAsset(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {

	assetBytes, exists, err := c.getAssetFromWorldState(stub, c.Prefix+args[0])
	if err != nil {
		err := fmt.Errorf("ReadAsset for class %s asset %s read from world state returned error %s", c.Name, args[0], err)
		log.Errorf(err.Error())
		return nil, err
	}
	if !exists {
		err := fmt.Errorf("ReadAsset for class %s asset %s asset does not exist", c.Name, args[0])
		log.Errorf(err.Error())
		return nil, err
	}
	return assetBytes, nil
}

// // ReadAssetUnmarshalled returns the asset from world state as an object, intended for internal use
// func ReadAssetUnmarshalled(stub shim.ChaincodeStubInterface, assetID string, assetName string, caller string) (interface{}, error) {
//     assetBytes, err := assetIsActive(stub, assetID)
//     if err != nil || len(assetBytes) == 0 {
//         return nil, err
//     }
//     var state interface{}
//     err = json.Unmarshal(assetBytes, &state)
//     if err != nil {
//         err = fmt.Errorf("readAssetUnmarshalled unmarshal failed: %s", err)
//         log.Errorf(err.Error())
//         return nil, err
//     }
//     return state, nil
// }

// ReadAllAssets returns all assets of a specific class from world state as an array
func (c AssetClass) ReadAllAssets(stub shim.ChaincodeStubInterface, args []string) ([]byte, error) {
	results, err := c.ReadAllAssetsUnmarshalled(stub, args)
	if err != nil {
		return nil, err
	}
	resultsBytes, err := json.Marshal(&results)
	if err != nil {
		err = fmt.Errorf("readAllAssets failed to marshal assets structure: %s", err)
		log.Errorf(err.Error())
		return nil, err
	}
	return resultsBytes, nil
}

// ReadAllAssetsUnmarshalled returns all assets of a specific class from world state as an object, intended for internal use
func (c AssetClass) ReadAllAssetsUnmarshalled(stub shim.ChaincodeStubInterface, args []string) (AssetArray, error) {
	var assets AssetArray
	var err error
	var filter StateFilter

	filter = getUnmarshalledStateFilter(stub, "ReadAllAssetsUnmarshalled", args)
	//log.Debugf("%s: got filter: %+v from args %+v\n", caller, filter, args)

	iter, err := stub.RangeQueryState(c.Prefix, c.Prefix+"}")
	if err != nil {
		err = fmt.Errorf("readAllAssetsUnmarshalled failed to get a range query iterator: %s", err)
		log.Errorf(err.Error())
		return nil, err
	}
	defer iter.Close()
	for iter.HasNext() {
		key, assetBytes, err := iter.Next()
		log.Debugf("ReadAllAssetsUnmarshalled iter k:%s v:%s\n", key, st.PrettyPrint(string(assetBytes)))
		if err != nil {
			err = fmt.Errorf("readAllAssetsUnmarshalled iter.Next() failed: %s", err)
			log.Errorf(err.Error())
			return nil, err
		}
		var state = new(Asset)
		err = json.Unmarshal(assetBytes, state)
		if err != nil {
			err = fmt.Errorf("readAllAssetsUnmarshalled unmarshal failed: %s", err)
			log.Errorf(err.Error())
			return nil, err
		}
		log.Debugf("%s: about to filter state: %+v using %+v\n", state, filter)
		if len(filter.Entries) == 0 || state.Filter(filter) {
			log.Debugf("%s: Filter PASSED\n", key)
			assets = append(assets, *state)
		}
	}

	//log.Debugf("%s: Final assets list: %+v\n", caller, assets)
	if len(assets) == 0 {
		return make(AssetArray, 0), nil
	}

	sort.Sort(assets)

	return assets, nil
}

// // ReadAssetHistory returns an asset's history from world state as an array
// func ReadAssetHistory(stub shim.ChaincodeStubInterface, args []string, assetName string, caller string) ([]byte, error) {
//     argsMap, err := getUnmarshalledArgument(stub, caller, args)
//     if err != nil {
//         return nil, err
//     }
//     assetID, err := validateAssetID(caller, assetName, argsMap)
//     if err != nil {
//         return nil, err
//     }
//     stateHistory, err := h.ReadStateHistory(stub, assetID)
//     if err != nil {
//         return nil, err
//     }
//     // is count present?
//     var olen int
//     countBytes, found := st.GetObject(argsMap, "count")
//     if found {
//         olen = int(countBytes.(float64))
//     }
//     if olen <= 0 || olen > len(stateHistory.AssetHistory) {
//         olen = len(stateHistory.AssetHistory)
//     }
//     var hStatesOut = make([]interface{}, 0, olen)
//     for i := 0; i < olen; i++ {
//         var obj interface{}
//         err = json.Unmarshal([]byte(stateHistory.AssetHistory[i]), &obj)
//         if err != nil {
//             log.Errorf("readAssetHistory JSON unmarshal of entry %d failed [%#v]", i, stateHistory.AssetHistory[i])
//             return nil, err
//         }
//         hStatesOut = append(hStatesOut, obj)
//     }
//     assetBytes, err := json.Marshal(hStatesOut)
//     if err != nil {
//         log.Errorf("readAssetHistory failed to marshal results: %s", err)
//         return nil, err
//     }

//     return []byte(assetBytes), nil
// }

//********** sort interface for AssetArray

func (aa AssetArray) Len() int           { return len(aa) }
func (aa AssetArray) Swap(i, j int)      { aa[i], aa[j] = aa[j], aa[i] }
func (aa AssetArray) Less(i, j int) bool { return aa[i].AssetKey < aa[j].AssetKey }
