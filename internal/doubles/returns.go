package doubles

import (
	"slices"
	"strings"
)

// classifyReturn maps a declared return type to its runtime check and to the
// type name that the check reports. It returns ReturnCheckUnsupported when no
// check can be proven for the type.
func (state *analysis) classifyReturn(returnType string) (ReturnCheck, string) {
	switch {
	case returnType == "void":
		return ReturnCheckVoid, ""
	case returnType == "Variant":
		return ReturnCheckAny, ""
	case slices.Contains(builtinTypeNames, returnType):
		return ReturnCheckBuiltin, returnType
	case isTypedCollection(returnType, "Array"):
		return ReturnCheckTypedArray, returnType
	case isTypedCollection(returnType, "Dictionary"):
		return ReturnCheckTypedDictionary, returnType
	}
	if slices.Contains(globalEnumNames, returnType) {
		return ReturnCheckEnum, returnType
	}
	head, tail, qualified := strings.Cut(returnType, ".")
	if !qualified {
		if state.enumNames[returnType] {
			return ReturnCheckEnum, returnType
		}
		return ReturnCheckObject, returnType
	}
	owner, known := state.index.classes[head]
	switch {
	case known && owner.enums[tail]:
		return ReturnCheckEnum, returnType
	case known && owner.innerClasses[tail]:
		return ReturnCheckObject, returnType
	}
	return ReturnCheckUnsupported, ""
}

func isTypedCollection(returnType string, collection string) bool {
	return strings.HasPrefix(returnType, collection+"[") && strings.HasSuffix(returnType, "]")
}
