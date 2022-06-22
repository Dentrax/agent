package ctyencode

import (
	"math/big"
	"reflect"

	"github.com/grafana/agent/pkg/river/vm/internal/rivertags"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/set"
)

// Encode produces a cty.Value from a Go value. The result will conform
// to the given type, or an error will be returned if this is not possible.
//
// The target type serves as a hint to resolve ambiguities in the mapping.
// For example, the Go type set.Set tells us that the value is a set but
// does not describe the set's element type. This also allows for convenient
// conversions, such as populating a set from a slice rather than having to
// first explicitly instantiate a set.Set.
//
// The audience of this function is assumed to be the developers of Go code
// that is integrating with cty, and thus the error messages it returns are
// presented from Go's perspective. These messages are thus not appropriate
// for display to end-users. An error returned from Encode represents a
// bug in the calling program, not user error.
func Encode(val interface{}, ty cty.Type) (cty.Value, error) {
	// 'path' starts off as empty but will grow for each level of recursive
	// call we make, so by the time toCtyValue returns it is likely to have
	// unused capacity on the end of it, depending on how deeply-recursive
	// the given Type is.
	path := make(cty.Path, 0)
	return encode(reflect.ValueOf(val), ty, path)
}

func encode(val reflect.Value, ty cty.Type, path cty.Path) (cty.Value, error) {
	if val != (reflect.Value{}) && val.Type().AssignableTo(valueType) {
		// If the source value is a cty.Value then we'll try to just pass
		// through to the target type directly.
		return encodePassthruogh(val, ty, path)
	}

	switch ty {
	case cty.Bool:
		return encodeBool(val, path)
	case cty.Number:
		return encodeNumber(val, path)
	case cty.String:
		return encodeString(val, path)
	case cty.DynamicPseudoType:
		return encodeDynamic(val, path)
	}

	switch {
	case ty.IsListType():
		return encodeList(val, ty.ElementType(), path)
	case ty.IsMapType():
		return encodeMap(val, ty.ElementType(), path)
	case ty.IsSetType():
		return encodeSet(val, ty.ElementType(), path)
	case ty.IsObjectType():
		return encodeObject(val, ty.AttributeTypes(), path)
	case ty.IsTupleType():
		return encodeTuple(val, ty.TupleElementTypes(), path)
	case ty.IsCapsuleType():
		return encodeCapsule(val, ty, path)
	}

	// We should never fall out here
	return cty.NilVal, path.NewErrorf("unsupported target type %#v", ty)
}

func encodeBool(val reflect.Value, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.Bool), nil
	}

	switch val.Kind() {

	case reflect.Bool:
		return cty.BoolVal(val.Bool()), nil

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to bool", val.Kind())

	}

}

func encodeNumber(val reflect.Value, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.Number), nil
	}

	switch val.Kind() {

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cty.NumberIntVal(val.Int()), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return cty.NumberUIntVal(val.Uint()), nil

	case reflect.Float32, reflect.Float64:
		return cty.NumberFloatVal(val.Float()), nil

	case reflect.Struct:
		if val.Type().AssignableTo(bigIntType) {
			bigInt := val.Interface().(big.Int)
			bigFloat := (&big.Float{}).SetInt(&bigInt)
			val = reflect.ValueOf(*bigFloat)
		}

		if val.Type().AssignableTo(bigFloatType) {
			bigFloat := val.Interface().(big.Float)
			return cty.NumberVal(&bigFloat), nil
		}

		fallthrough
	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to number", val.Kind())

	}

}

func encodeString(val reflect.Value, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.String), nil
	}

	switch val.Kind() {

	case reflect.String:
		return cty.StringVal(val.String()), nil

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to string", val.Kind())

	}

}

func encodeList(val reflect.Value, ety cty.Type, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.List(ety)), nil
	}

	switch val.Kind() {

	case reflect.Slice:
		if val.IsNil() {
			return cty.NullVal(cty.List(ety)), nil
		}
		fallthrough
	case reflect.Array:
		if val.Len() == 0 {
			return cty.ListValEmpty(ety), nil
		}

		// While we work on our elements we'll temporarily grow
		// path to give us a place to put our index step.
		path = append(path, cty.PathStep(nil))

		vals := make([]cty.Value, val.Len())
		for i := range vals {
			var err error
			path[len(path)-1] = cty.IndexStep{
				Key: cty.NumberIntVal(int64(i)),
			}
			vals[i], err = encode(val.Index(i), ety, path)
			if err != nil {
				return cty.NilVal, err
			}
		}

		// Discard our extra path segment, retaining it as extra capacity
		// for future appending to the path.
		path = path[:len(path)-1]

		return cty.ListVal(vals), nil

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to %#v", val.Kind(), cty.List(ety))

	}
}

func encodeMap(val reflect.Value, ety cty.Type, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.Map(ety)), nil
	}

	switch val.Kind() {

	case reflect.Map:
		if val.IsNil() {
			return cty.NullVal(cty.Map(ety)), nil
		}

		if val.Len() == 0 {
			return cty.MapValEmpty(ety), nil
		}

		keyType := val.Type().Key()
		if keyType.Kind() != reflect.String {
			return cty.NilVal, path.NewErrorf("can't convert Go map with key type %s; key type must be string", keyType)
		}

		// While we work on our elements we'll temporarily grow
		// path to give us a place to put our index step.
		path = append(path, cty.PathStep(nil))

		vals := make(map[string]cty.Value, val.Len())
		for _, kv := range val.MapKeys() {
			k := kv.String()
			var err error
			path[len(path)-1] = cty.IndexStep{
				Key: cty.StringVal(k),
			}
			vals[k], err = encode(val.MapIndex(reflect.ValueOf(k)), ety, path)
			if err != nil {
				return cty.NilVal, err
			}
		}

		// Discard our extra path segment, retaining it as extra capacity
		// for future appending to the path.
		path = path[:len(path)-1]

		return cty.MapVal(vals), nil

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to %#v", val.Kind(), cty.Map(ety))

	}
}

func encodeSet(val reflect.Value, ety cty.Type, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.Set(ety)), nil
	}

	var vals []cty.Value

	switch val.Kind() {

	case reflect.Slice:
		if val.IsNil() {
			return cty.NullVal(cty.Set(ety)), nil
		}
		fallthrough
	case reflect.Array:
		if val.Len() == 0 {
			return cty.SetValEmpty(ety), nil
		}

		vals = make([]cty.Value, val.Len())
		for i := range vals {
			var err error
			vals[i], err = encode(val.Index(i), ety, path)
			if err != nil {
				return cty.NilVal, err
			}
		}

	case reflect.Struct:

		if !val.Type().AssignableTo(setType) {
			return cty.NilVal, path.NewErrorf("can't convert Go %s to %#v", val.Type(), cty.Set(ety))
		}

		rawSet := val.Interface().(set.Set)
		inVals := rawSet.Values()

		if len(inVals) == 0 {
			return cty.SetValEmpty(ety), nil
		}

		vals = make([]cty.Value, len(inVals))
		for i := range inVals {
			var err error
			vals[i], err = encode(reflect.ValueOf(inVals[i]), ety, path)
			if err != nil {
				return cty.NilVal, err
			}
		}

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to %#v", val.Kind(), cty.Set(ety))

	}

	return cty.SetVal(vals), nil
}

func encodeObject(val reflect.Value, attrTypes map[string]cty.Type, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.Object(attrTypes)), nil
	}

	switch val.Kind() {

	case reflect.Map:
		if val.IsNil() {
			return cty.NullVal(cty.Object(attrTypes)), nil
		}

		keyType := val.Type().Key()
		if keyType.Kind() != reflect.String {
			return cty.NilVal, path.NewErrorf("can't convert Go map with key type %s; key type must be string", keyType)
		}

		if len(attrTypes) == 0 {
			return cty.EmptyObjectVal, nil
		}

		// While we work on our elements we'll temporarily grow
		// path to give us a place to put our GetAttr step.
		path = append(path, cty.PathStep(nil))

		haveKeys := make(map[string]struct{}, val.Len())
		for _, kv := range val.MapKeys() {
			haveKeys[kv.String()] = struct{}{}
		}

		vals := make(map[string]cty.Value, len(attrTypes))
		for k, at := range attrTypes {
			var err error
			path[len(path)-1] = cty.GetAttrStep{
				Name: k,
			}

			if _, have := haveKeys[k]; !have {
				vals[k] = cty.NullVal(at)
				continue
			}

			vals[k], err = encode(val.MapIndex(reflect.ValueOf(k)), at, path)
			if err != nil {
				return cty.NilVal, err
			}
		}

		// Discard our extra path segment, retaining it as extra capacity
		// for future appending to the path.
		path = path[:len(path)-1]

		return cty.ObjectVal(vals), nil

	case reflect.Struct:
		if len(attrTypes) == 0 {
			return cty.EmptyObjectVal, nil
		}

		// While we work on our elements we'll temporarily grow
		// path to give us a place to put our GetAttr step.
		path = append(path, cty.PathStep(nil))

		attrFields := rivertags.Get(val.Type())

		vals := make(map[string]cty.Value, len(attrTypes))
		for k, at := range attrTypes {
			path[len(path)-1] = cty.GetAttrStep{
				Name: k,
			}

			if f, ok := attrFields.Get(k); ok {
				var err error
				vals[k], err = encode(val.Field(f.Index), at, path)
				if err != nil {
					return cty.NilVal, err
				}
			} else {
				vals[k] = cty.NullVal(at)
			}
		}

		// Discard our extra path segment, retaining it as extra capacity
		// for future appending to the path.
		path = path[:len(path)-1]

		return cty.ObjectVal(vals), nil

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to %#v", val.Kind(), cty.Object(attrTypes))

	}
}

func encodeTuple(val reflect.Value, elemTypes []cty.Type, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.Tuple(elemTypes)), nil
	}

	switch val.Kind() {

	case reflect.Slice:
		if val.IsNil() {
			return cty.NullVal(cty.Tuple(elemTypes)), nil
		}

		if val.Len() != len(elemTypes) {
			return cty.NilVal, path.NewErrorf("wrong number of elements %d; need %d", val.Len(), len(elemTypes))
		}

		if len(elemTypes) == 0 {
			return cty.EmptyTupleVal, nil
		}

		// While we work on our elements we'll temporarily grow
		// path to give us a place to put our Index step.
		path = append(path, cty.PathStep(nil))

		vals := make([]cty.Value, len(elemTypes))
		for i, ety := range elemTypes {
			var err error

			path[len(path)-1] = cty.IndexStep{
				Key: cty.NumberIntVal(int64(i)),
			}

			vals[i], err = encode(val.Index(i), ety, path)
			if err != nil {
				return cty.NilVal, err
			}
		}

		// Discard our extra path segment, retaining it as extra capacity
		// for future appending to the path.
		path = path[:len(path)-1]

		return cty.TupleVal(vals), nil

	case reflect.Struct:
		fieldCount := val.Type().NumField()
		if fieldCount != len(elemTypes) {
			return cty.NilVal, path.NewErrorf("wrong number of struct fields %d; need %d", fieldCount, len(elemTypes))
		}

		if len(elemTypes) == 0 {
			return cty.EmptyTupleVal, nil
		}

		// While we work on our elements we'll temporarily grow
		// path to give us a place to put our Index step.
		path = append(path, cty.PathStep(nil))

		vals := make([]cty.Value, len(elemTypes))
		for i, ety := range elemTypes {
			var err error

			path[len(path)-1] = cty.IndexStep{
				Key: cty.NumberIntVal(int64(i)),
			}

			vals[i], err = encode(val.Field(i), ety, path)
			if err != nil {
				return cty.NilVal, err
			}
		}

		// Discard our extra path segment, retaining it as extra capacity
		// for future appending to the path.
		path = path[:len(path)-1]

		return cty.TupleVal(vals), nil

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s to %#v", val.Kind(), cty.Tuple(elemTypes))

	}
}

func encodeCapsule(val reflect.Value, capsuleType cty.Type, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(capsuleType), nil
	}

	if val.Kind() != reflect.Ptr {
		if !val.CanAddr() {
			return cty.NilVal, path.NewErrorf("source value for capsule %#v must be addressable", capsuleType)
		}

		val = val.Addr()
	}

	if !val.Type().Elem().AssignableTo(capsuleType.EncapsulatedType()) {
		return cty.NilVal, path.NewErrorf("value of type %T not compatible with capsule %#v", val.Interface(), capsuleType)
	}

	return cty.CapsuleVal(capsuleType, val.Interface()), nil
}

func encodeDynamic(val reflect.Value, path cty.Path) (cty.Value, error) {
	if val = unwrapPointer(val); !val.IsValid() {
		return cty.NullVal(cty.DynamicPseudoType), nil
	}

	switch val.Kind() {

	case reflect.Struct:
		if !val.Type().AssignableTo(valueType) {
			return cty.NilVal, path.NewErrorf("can't convert Go %s dynamically; only cty.Value allowed", val.Type())
		}

		return val.Interface().(cty.Value), nil

	default:
		return cty.NilVal, path.NewErrorf("can't convert Go %s dynamically; only cty.Value allowed", val.Kind())

	}

}

func encodePassthruogh(wrappedVal reflect.Value, wantTy cty.Type, path cty.Path) (cty.Value, error) {
	if wrappedVal = unwrapPointer(wrappedVal); !wrappedVal.IsValid() {
		return cty.NullVal(wantTy), nil
	}

	givenVal := wrappedVal.Interface().(cty.Value)

	val, err := convert.Convert(givenVal, wantTy)
	if err != nil {
		return cty.NilVal, path.NewErrorf("unsuitable value: %s", err)
	}
	return val, nil
}

// unwrapPointer is a helper for dealing with Go pointers. It has three
// possible outcomes:
//
// - Given value isn't a pointer, so it's just returned as-is.
// - Given value is a non-nil pointer, in which case it is dereferenced
//   and the result returned.
// - Given value is a nil pointer, in which case an invalid value is returned.
//
// For nested pointer types, like **int, they are all dereferenced in turn
// until a non-pointer value is found, or until a nil pointer is encountered.
func unwrapPointer(val reflect.Value) reflect.Value {
	for val.Kind() == reflect.Ptr || val.Kind() == reflect.Interface {
		if val.IsNil() {
			return reflect.Value{}
		}

		val = val.Elem()
	}

	return val
}
