package compiler

func Check(root string) (*Result, error) {
	return Compile(root)
}
