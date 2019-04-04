// Copyright © 2019 Harrison Brown harrybrown98@gmail.com
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/harrybrwn/apizza/cmd/internal/base"
	"github.com/harrybrwn/apizza/dawg"
)

type menuCmd struct {
	*basecmd
	all            bool
	toppings       bool
	preconfigured  bool
	showCategories bool
	item           string
	category       string
}

func (c *menuCmd) Run(cmd *cobra.Command, args []string) error {
	if err := db.UpdateTS("menu", c); err != nil {
		return err
	}

	var item dawg.Item

	if len(args) == 1 {
		if c.item == "" {
			item = c.menu.FindItem(args[0])
		}

		if item == nil && c.category == "" {
			c.category = strings.ToLower(args[0])
		} else {
			iteminfo(item, c.Output())
			return nil
		}
	}

	if c.item != "" {
		prod := c.menu.FindItem(c.item)
		if prod == nil {
			return fmt.Errorf("cannot find %s", c.item)
		}
		iteminfo(prod, cmd.OutOrStdout())
		return nil
	}

	if c.toppings {
		c.printToppings()
		return nil
	}

	// print menu handles most of the menu command's flags
	return c.printMenu(strings.ToLower(c.category)) // still works with an empty string
}

func (b *cliBuilder) newMenuCmd() base.CliCommand {
	c := &menuCmd{
		all: false, toppings: false,
		preconfigured: false, showCategories: false}
	c.basecmd = b.newCommand("menu <item>", "Get the Dominos menu.", c)

	c.Flags().StringVarP(&c.item, "item", "i", "", "show info on the menu item given")
	c.Flags().StringVarP(&c.category, "category", "c", "", "show one category on the menu")

	c.Flags().BoolVarP(&c.all, "all", "a", c.all, "show the entire menu")
	c.Flags().BoolVarP(&c.toppings, "toppings", "t", c.toppings, "print out the toppings on the menu")
	c.Flags().BoolVarP(&c.preconfigured, "preconfigured",
		"p", c.preconfigured, "show the pre-configured products on the dominos menu")
	c.Flags().BoolVar(&c.showCategories, "show-categories", c.showCategories, "print categories")
	return c
}

func (c *menuCmd) printMenu(categoryName string) error {
	var printfunc func(dawg.MenuCategory, int) error

	printfunc = func(cat dawg.MenuCategory, depth int) error {
		if cat.IsEmpty() {
			return nil
		}
		c.Printf("%s%s\n", strings.Repeat("  ", depth), cat.Name)

		if cat.HasItems() {
			for _, p := range cat.Products {
				c.printCategory(p, depth+1)
			}
		} else {
			for _, category := range cat.Categories {
				printfunc(category, depth+1)
			}
		}
		return nil
	}

	var allCategories = c.menu.Categorization.Food.Categories

	if c.preconfigured {
		allCategories = c.menu.Categorization.Preconfigured.Categories
	} else if c.all {
		allCategories = append(allCategories, c.menu.Categorization.Preconfigured.Categories...)
	}

	if len(categoryName) > 0 {
		for _, cat := range allCategories {
			if categoryName == strings.ToLower(cat.Name) || categoryName == strings.ToLower(cat.Code) {
				return printfunc(cat, 0)
			}
		}
		return fmt.Errorf("could not find %s", categoryName)
	} else if c.showCategories {
		for _, cat := range allCategories {
			if cat.Name != "" {
				c.Println(strings.ToLower(cat.Name))
			}
		}
		return nil
	}

	for _, c := range allCategories {
		printfunc(c, 0)
	}
	return nil
}

func (c *menuCmd) printCategory(code string, indentLen int) {
	item := c.menu.FindItem(code)

	switch product := item.(type) {
	case *dawg.Product:
		if len(product.Variants) == 1 {
			p, err := c.menu.GetVariant(product.Variants[0])
			if err != nil {
				panic(err)
			}
			c.Printf("%s%s  %s\n", strings.Repeat("  ", indentLen), p.Code, p.Name)
			break
		}
		c.Printf("%s%s [%s]\n", strings.Repeat("  ", indentLen),
			item.ItemName(), item.ItemCode())
		n := maxStrLen(product.Variants)
		for _, variant := range product.Variants {
			v, err := c.menu.GetVariant(variant)
			if err != nil {
				continue
			}
			c.Printf("%s%s %s %s\n",
				strings.Repeat("  ", indentLen+1), variant,
				strings.Repeat(" ", n-strLen(variant)), v.Name)
		}

	case *dawg.PreConfiguredProduct:
		c.Printf("%s%s   %s\n", strings.Repeat("  ", indentLen),
			item.ItemCode(), item.ItemName())
	default:
		panic("dawg.Product and dawg.PreConfiguredProduct are the only catagories to be printed")
	}
}

func iteminfo(prod dawg.Item, w io.Writer) {
	o := &bytes.Buffer{}

	fmt.Fprintf(o, "%s ", prod.ItemName())

	switch p := prod.(type) {
	case *dawg.Variant:
		fmt.Fprintf(o, "[%s] - (variant)\n", prod.ItemCode())
		fmt.Fprintf(o, "  price: %s\n", p.Price)
		prod := p.GetProduct()
		if defTops, ok := p.Tags["DefaultToppings"]; ok {
			fmt.Fprintf(o, "  default toppings: %s\n", defTops)
		}
		if prod != nil {
			fmt.Fprintf(o, "  parent: %s - %s\n", prod.ItemName(), prod.ItemCode())
		}

	case *dawg.PreConfiguredProduct:
		fmt.Fprintf(o, "[%s] - (preconfigured product)\n", prod.ItemCode())
		fmt.Fprintf(o, "  description: %s\n", p.Description)
		fmt.Fprintf(o, "  size:        %s\n", p.Size)

	case *dawg.Product:
		fmt.Fprintf(o, "[%s] - (product category)\n", prod.ItemCode())
		fmt.Fprintf(o, "  description: %s\n", p.Description)
		fmt.Fprintf(o, "  avalable sides: %s\n", p.AvailableSides)
		fmt.Fprintf(o, "  avalable toppings: %s\n", p.AvailableToppings)
	}

	if _, err := w.Write(o.Bytes()); err != nil {
		panic(err)
	}
}

func (c *menuCmd) printToppings() {
	var tops = c.menu.Toppings

	if c.category != "" {
		category := strings.Title(c.category)
		printToppingCategory(category, tops[category], c.Output())
		return
	}

	if c.showCategories {
		for cat := range tops {
			c.Println(strings.ToLower(cat))
		}
		return
	}

	for typ, toppings := range tops {
		printToppingCategory(typ, toppings, c.Output())
	}
}

func printToppingCategory(name string, toppings map[string]dawg.Topping, w io.Writer) {
	fmt.Fprintln(w, "  ", name)
	indent := strings.Repeat(" ", 4)
	for k, v := range toppings {
		fmt.Fprintln(w, indent, k, strings.Repeat(" ", 3-strLen(k)), v.Name)
	}
	fmt.Fprintln(w, "")
}

func maxStrLen(list []string) int {
	max := 0
	for _, s := range list {
		length := strLen(s)
		if length > max {
			max = length
		}
	}

	return max
}

var strLen = utf8.RuneCountInString // this is a function

func spaces(i int) string {
	return strings.Repeat(" ", i)
}
