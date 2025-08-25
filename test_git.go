package main
import ("fmt"; "os")
func main() {
    version, err := getCurrentSourceVersion("https://github.com/vim/vim.git", "git")
    fmt.Printf("Version: %s, Error: %v\n", version, err)
}
